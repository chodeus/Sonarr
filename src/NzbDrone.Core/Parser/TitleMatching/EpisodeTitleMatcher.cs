using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text.RegularExpressions;
using System.Threading;
using NLog;
using NzbDrone.Common.Cache;
using NzbDrone.Common.Extensions;
using NzbDrone.Core.Languages;
using NzbDrone.Core.Messaging.Events;
using NzbDrone.Core.Parser.Model;
using NzbDrone.Core.Tags;
using NzbDrone.Core.Tv;

namespace NzbDrone.Core.Parser.TitleMatching
{
    public interface IEpisodeTitleMatcher
    {
        // Null keeps the parsed numbers; a result without numbers means the release names no episode of the series
        ParsedEpisodeInfo ParseEventTitle(ParsedEpisodeInfo parsedEpisodeInfo, Series series);
    }

    public class EpisodeTitleMatcher : IEpisodeTitleMatcher, IHandle<TagsUpdatedEvent>
    {
        public const string TagLabel = "title-match";

        private static readonly Regex YearRegex = new(@"\b(?<year>(?:19|20)\d{2})(?:x(?<round>\d{1,2}))?\b", RegexOptions.Compiled);
        private static readonly Regex RoundRegex = new(@"\b(?:round ?|r)(?<round>\d{1,2})\b", RegexOptions.Compiled);
        private static readonly Regex GenericEventWords = new(@"\b(?:grand prix|gp|formula 1|formula1|f1)\b|\b(?:19|20)\d{2}\b", RegexOptions.Compiled);
        private static readonly Regex Spaces = new(@"\s+", RegexOptions.Compiled);
        private static readonly Regex ExplicitEpisodeNumbering = new(@"\bs\d{1,4} ?e\d{1,4}\b", RegexOptions.Compiled);
        private static readonly HashSet<string> LanguagePlaceNames = TitleMatchVocabulary.Places
            .SelectMany(p => p)
            .Where(n => LanguageParser.ParseLanguages(n).Any(l => l != Language.Unknown))
            .ToHashSet();

        // A weekend's sessions sit at most two days apart, even with TheTVDB's odd misdated one; one venue's next weekend
        // is five or more away (Bahrain and Sakhir, 2020)
        private static readonly TimeSpan WeekendGap = TimeSpan.FromDays(3);

        private readonly IEpisodeService _episodeService;
        private readonly ITagRepository _tagRepository;
        private readonly ICached<Tag> _tagCache;
        private readonly Logger _logger;
        private int _tagVersion;

        public EpisodeTitleMatcher(IEpisodeService episodeService,
                                   ITagRepository tagRepository,
                                   ICacheManager cacheManager,
                                   Logger logger)
        {
            _episodeService = episodeService;
            _tagRepository = tagRepository;
            _tagCache = cacheManager.GetCache<Tag>(GetType());
            _logger = logger;
        }

        public ParsedEpisodeInfo ParseEventTitle(ParsedEpisodeInfo parsedEpisodeInfo, Series series)
        {
            if (parsedEpisodeInfo?.ReleaseTitle == null || !IsTitleMatchSeries(series))
            {
                return null;
            }

            // A special found by its TheTVDB title already names its episode
            if (parsedEpisodeInfo.Special && parsedEpisodeInfo.SeasonNumbers.FirstOrDefault(-1) == 0)
            {
                return null;
            }

            var release = TitleMatchVocabulary.Normalize(parsedEpisodeInfo.ReleaseTitle);
            var episode = FindEpisode(series, release, out var eventNames);

            if (episode == null)
            {
                // "S2026E94" is TheTVDB's numbering; in "2026x17" the 17 is the round, unless the name also carries that episode's title
                if (ExplicitEpisodeNumbering.IsMatch(release) || NamesParsedEpisodes(parsedEpisodeInfo, series, release))
                {
                    return null;
                }

                _logger.Debug("'{0}' names no event of {1}, so it isn't mapped", parsedEpisodeInfo.ReleaseTitle, series.Title);

                return WithoutNumbers(parsedEpisodeInfo);
            }

            _logger.Debug("Matched '{0}' to {1}x{2:00} '{3}' by title", parsedEpisodeInfo.ReleaseTitle, episode.SeasonNumber, episode.EpisodeNumber, episode.Title);

            var eventEpisodeInfo = WithNumbers(parsedEpisodeInfo, [episode.SeasonNumber], [episode.EpisodeNumber]);
            eventEpisodeInfo.Languages = WithoutEventLanguages(parsedEpisodeInfo.Languages, release, eventNames);

            return eventEpisodeInfo;
        }

        public void Handle(TagsUpdatedEvent message)
        {
            Interlocked.Increment(ref _tagVersion);
            _tagCache.Clear();
        }

        // Keeps the parse's flags, so a special or season pack is still handled as one
        private static ParsedEpisodeInfo WithoutNumbers(ParsedEpisodeInfo parsedEpisodeInfo)
        {
            var eventEpisodeInfo = parsedEpisodeInfo.JsonClone();
            eventEpisodeInfo.SeasonNumbers = [];
            eventEpisodeInfo.EpisodeNumbers = [];
            eventEpisodeInfo.AbsoluteEpisodeNumbers = [];
            eventEpisodeInfo.SpecialAbsoluteEpisodeNumbers = [];
            eventEpisodeInfo.AirDate = null;
            eventEpisodeInfo.DailyPart = null;

            return eventEpisodeInfo;
        }

        private static ParsedEpisodeInfo WithNumbers(ParsedEpisodeInfo parsedEpisodeInfo, List<int> seasonNumbers, int[] episodeNumbers)
        {
            var eventEpisodeInfo = WithoutNumbers(parsedEpisodeInfo);
            eventEpisodeInfo.SeasonNumbers = seasonNumbers;
            eventEpisodeInfo.EpisodeNumbers = episodeNumbers;
            eventEpisodeInfo.FullSeason = false;
            eventEpisodeInfo.IsPartialSeason = false;
            eventEpisodeInfo.IsSeasonExtra = false;
            eventEpisodeInfo.IsSeasonTitle = false;
            eventEpisodeInfo.IsSplitEpisode = false;
            eventEpisodeInfo.IsMiniSeries = false;
            eventEpisodeInfo.SeasonPart = 0;
            eventEpisodeInfo.Special = false;

            return eventEpisodeInfo;
        }

        private Episode FindEpisode(Series series, string release, out string[] eventNames)
        {
            eventNames = null;

            if (!IsSingleEvent(release, TitleMatchVocabulary.Normalize(series.Title)))
            {
                return null;
            }

            var releaseSession = ParseSession(release, out _, out var sessionCount);

            // With no session word the release is the race only when it names the grand prix; otherwise it's an unlisted programme
            if (sessionCount > 1 || (releaseSession == null && !TitleMatchVocabulary.GrandPrix.IsMatch(release)))
            {
                return null;
            }

            var weekends = FindWeekends(series, release, out var round);
            Func<string, bool> namesEvent = n => MentionsPlace(release, n);
            var matchedWeekends = weekends.Where(w => w.Names.Any(namesEvent)).ToList();

            // TheTVDB's weekends can drift from the real rounds, so a round confirms or narrows a named place, never picks one
            if (round.HasValue)
            {
                if (round.Value < 1 || round.Value > weekends.Count)
                {
                    return null;
                }

                var roundWeekend = weekends[round.Value - 1];

                // A place that is also a language ("Japan") may be the audio, so it names the event once the round agrees
                if (matchedWeekends.Empty())
                {
                    // Only the first of its names is the event, so a later one ("Japan ... Japanese") stays the audio
                    var firstName = roundWeekend.Names.Where(n => TitleMatchVocabulary.ContainsPhrase(release, n))
                                                      .OrderBy(n => $" {release} ".IndexOf($" {n} ", StringComparison.Ordinal))
                                                      .FirstOrDefault();

                    namesEvent = n => n == firstName;
                    matchedWeekends = firstName != null ? new List<Weekend> { roundWeekend } : matchedWeekends;
                }

                if (!matchedWeekends.Contains(roundWeekend))
                {
                    return null;
                }

                matchedWeekends = new List<Weekend> { roundWeekend };
            }
            else if (matchedWeekends.Count > 1)
            {
                // Weekends can share a venue's names (Bahrain and Sakhir, 2020); TheTVDB's own place tells them apart
                var byOwnPlace = matchedWeekends.Where(w => w.Places.Any(p => TitleMatchVocabulary.ContainsPhrase(release, p))).ToList();
                matchedWeekends = byOwnPlace.Count == 1 ? byOwnPlace : matchedWeekends;
            }

            if (matchedWeekends.Count != 1)
            {
                return null;
            }

            var matchedWeekend = matchedWeekends.Single();
            var matches = matchedWeekend.Events.Where(e => SessionMatches(releaseSession, e, matchedWeekend)).ToList();

            if (matches.Count != 1)
            {
                return null;
            }

            var episode = matches.Single().Episode;

            // A release can't predate its event, but TheTVDB times every session at the series' air time, hours after
            // an Asia-Pacific session has aired, so allow a day.
            if (!episode.AirDateUtc.HasValue || episode.AirDateUtc.Value > DateTime.UtcNow.AddDays(1))
            {
                return null;
            }

            eventNames = matchedWeekend.Names.Where(namesEvent).ToArray();

            return episode;
        }

        private bool NamesParsedEpisodes(ParsedEpisodeInfo parsedEpisodeInfo, Series series, string release)
        {
            if (parsedEpisodeInfo.SeasonNumbers.Empty() || parsedEpisodeInfo.EpisodeNumbers.Empty())
            {
                return false;
            }

            var remainder = $" {release} ";

            foreach (var number in parsedEpisodeInfo.EpisodeNumbers)
            {
                var episode = _episodeService.FindEpisode(series.Id, parsedEpisodeInfo.SeasonNumbers.First(), number);
                var title = episode?.Title.IsNullOrWhiteSpace() == false ? TitleMatchVocabulary.Normalize(episode.Title) : null;

                if (title == null || !TitleMatchVocabulary.ContainsPhrase(release, title))
                {
                    return false;
                }

                remainder = remainder.Replace($" {title} ", " ");
            }

            // "2026x14 Australia Race Highlights" carries the title but is another programme
            return !NamesOtherProgramme(remainder.Trim(), TitleMatchVocabulary.Normalize(series.Title));
        }

        // A place name that is also a language ("italian") names the event only before "grand prix"; elsewhere it's the audio
        private static bool MentionsPlace(string release, string name)
        {
            return TitleMatchVocabulary.ContainsPhrase(release, name) &&
                   (!LanguagePlaceNames.Contains(name) ||
                    TitleMatchVocabulary.ContainsPhrase(release, $"{name} grand prix") ||
                    TitleMatchVocabulary.ContainsPhrase(release, $"{name} gp"));
        }

        // "Italian" in "Italian Grand Prix" names the event, not the audio, unless the release names it again.
        private static List<Language> WithoutEventLanguages(List<Language> languages, string release, string[] eventNames)
        {
            var placeNames = eventNames.ToList();

            // Only the first mention is the event; a second one is the release naming its language.
            var withoutPlaceNames = placeNames.Aggregate($" {release} ", (title, name) =>
            {
                var index = title.IndexOf($" {name} ", StringComparison.Ordinal);

                return index < 0 ? title : title.Remove(index, name.Length + 1);
            });

            var eventLanguages = placeNames
                .SelectMany(LanguageParser.ParseLanguages)
                .Where(l => l != Language.Unknown)
                .Except(LanguageParser.ParseLanguages(withoutPlaceNames))
                .ToList();

            var remaining = (languages ?? new List<Language>()).Except(eventLanguages).ToList();

            return remaining.Any() ? remaining : new List<Language> { Language.Unknown };
        }

        private bool IsTitleMatchSeries(Series series)
        {
            if (series?.Tags == null || series.Tags.Empty())
            {
                return false;
            }

            var version = Volatile.Read(ref _tagVersion);
            var tag = _tagCache.Get(TagLabel, () => _tagRepository.FindByLabel(TagLabel), TimeSpan.FromMinutes(5));

            // A tag change during the read may have cached what the read saw before it
            if (version != Volatile.Read(ref _tagVersion))
            {
                _tagCache.Remove(TagLabel);
            }

            return tag != null && series.Tags.Contains(tag.Id);
        }

        private static bool IsSingleEvent(string release, string seriesTitle)
        {
            return !TitleMatchVocabulary.Testing.IsMatch(release) && !NamesOtherProgramme(release, seriesTitle);
        }

        private static bool NamesOtherProgramme(string release, string seriesTitle)
        {
            return TitleMatchVocabulary.NonEventWords.Any(w => TitleMatchVocabulary.ContainsPhrase(release, w)) ||
                   TitleMatchVocabulary.SiblingSeriesWords.Any(w => TitleMatchVocabulary.ContainsPhrase(release, w) &&
                                                                    !TitleMatchVocabulary.ContainsPhrase(seriesTitle, w));
        }

        private List<Weekend> FindWeekends(Series series, string release, out int? round)
        {
            var yearMatches = YearRegex.Matches(release);
            var rounds = RoundRegex.Matches(release).Select(m => m.Groups["round"].Value)
                                   .Concat(yearMatches.Where(m => m.Groups["round"].Success).Select(m => m.Groups["round"].Value))
                                   .Select(r => int.Parse(r, CultureInfo.InvariantCulture))
                                   .Distinct()
                                   .ToList();
            var years = yearMatches.Select(m => int.Parse(m.Groups["year"].Value, CultureInfo.InvariantCulture)).Distinct().ToList();

            round = rounds.Count == 1 ? rounds[0] : null;

            // Two different rounds can't both be right
            if (years.Count != 1 || rounds.Count > 1)
            {
                return new List<Weekend>();
            }

            return GroupWeekends(_episodeService.GetEpisodesBySeason(series.Id, years[0]));
        }

        private static List<Weekend> GroupWeekends(List<Episode> episodes)
        {
            var events = episodes.Select(ParseEvent).Where(e => e != null).ToList();
            var weekends = new List<Weekend>();

            foreach (var place in events.GroupBy(e => e.Names[0]))
            {
                Weekend weekend = null;

                foreach (var titleEvent in place.OrderBy(e => e.AirDate))
                {
                    if (weekend == null || titleEvent.AirDate - weekend.Events.Last().AirDate > WeekendGap)
                    {
                        weekend = new Weekend(titleEvent.Names);
                        weekends.Add(weekend);
                    }

                    weekend.Events.Add(titleEvent);
                    weekend.Places.Add(titleEvent.Place);
                }
            }

            return weekends.OrderBy(w => w.Events[0].AirDate).ToList();
        }

        private static TitleEvent ParseEvent(Episode episode)
        {
            // An undated episode can't have aired, and would split its weekend in two
            if (episode.Title.IsNullOrWhiteSpace() || !episode.AirDateUtc.HasValue)
            {
                return null;
            }

            var title = TitleMatchVocabulary.Normalize(episode.Title);

            if (TitleMatchVocabulary.Testing.IsMatch(title))
            {
                return null;
            }

            var session = ParseSession(title, out var remainder, out var sessionCount);
            var place = Spaces.Replace(GenericEventWords.Replace(remainder, " "), " ").Trim();

            if (sessionCount > 1 || place.IsNullOrWhiteSpace())
            {
                return null;
            }

            return new TitleEvent(episode, session, place, PlaceNames(place));
        }

        private static string[] PlaceNames(string place)
        {
            var known = TitleMatchVocabulary.Places.FirstOrDefault(p => p.Contains(place)) ??
                        TitleMatchVocabulary.Places
                            .Where(p => p.Any(n => TitleMatchVocabulary.ContainsPhrase(place, n)))
                            .OrderByDescending(p => p.Where(n => TitleMatchVocabulary.ContainsPhrase(place, n)).Max(n => n.Length))
                            .FirstOrDefault();

            return known ?? new[] { place };
        }

        private static string ParseSession(string title, out string remainder, out int sessionCount)
        {
            string session = null;
            sessionCount = 0;
            remainder = title;

            foreach (var (name, pattern) in TitleMatchVocabulary.Sessions)
            {
                var match = pattern.Match(remainder);

                if (!match.Success)
                {
                    continue;
                }

                session = string.Format(name, match.Groups["number"].Value);
                sessionCount += pattern.Matches(remainder).Select(m => string.Format(name, m.Groups["number"].Value)).Distinct().Count();
                remainder = pattern.Replace(remainder, " ");
            }

            return session;
        }

        private static bool SessionMatches(string releaseSession, TitleEvent titleEvent, Weekend weekend)
        {
            if (releaseSession == null)
            {
                var mainSession = TitleMatchVocabulary.MainSessions.FirstOrDefault(s => weekend.Events.Any(e => e.Session == s));

                return titleEvent.Session == mainSession;
            }

            if (titleEvent.Session == releaseSession)
            {
                return true;
            }

            // Sprint weekends have one practice, which TheTVDB leaves unnumbered.
            var practices = weekend.Events.Where(e => e.Session != null && e.Session.StartsWith("practice", StringComparison.Ordinal)).ToList();

            return releaseSession is "practice 1" or "practice" && practices.Count == 1 && practices[0] == titleEvent;
        }

        private class TitleEvent
        {
            public TitleEvent(Episode episode, string session, string place, string[] names)
            {
                Episode = episode;
                Session = session;
                Place = place;
                Names = names;
                AirDate = episode.AirDateUtc.Value;
            }

            public Episode Episode { get; }
            public string Session { get; }
            public string Place { get; }
            public string[] Names { get; }
            public DateTime AirDate { get; }
        }

        private class Weekend
        {
            public Weekend(string[] names)
            {
                Names = names;
            }

            public string[] Names { get; }
            public HashSet<string> Places { get; } = new();
            public List<TitleEvent> Events { get; } = new();
        }
    }
}
