using System;
using System.Collections.Generic;
using System.Linq;
using System.Text.RegularExpressions;
using NLog;
using NzbDrone.Common.Cache;
using NzbDrone.Common.Extensions;
using NzbDrone.Core.Languages;
using NzbDrone.Core.Parser.Model;
using NzbDrone.Core.Tags;
using NzbDrone.Core.Tv;

namespace NzbDrone.Core.Parser.TitleMatching
{
    public interface IEpisodeTitleMatcher
    {
        ParsedEpisodeInfo ParseEventTitle(ParsedEpisodeInfo parsedEpisodeInfo, Series series);
    }

    public class EpisodeTitleMatcher : IEpisodeTitleMatcher
    {
        public const string TagLabel = "title-match";

        private static readonly Regex YearRegex = new(@"\b(?<year>(?:19|20)\d{2})(?:x(?<round>\d{1,2}))?\b", RegexOptions.Compiled);
        private static readonly Regex RoundRegex = new(@"\b(?:round ?|r)(?<round>\d{1,2})\b", RegexOptions.Compiled);
        private static readonly Regex GenericEventWords = new(@"\b(?:grand prix|gp|formula 1|formula1|f1)\b|\b(?:19|20)\d{2}\b", RegexOptions.Compiled);
        private static readonly Regex Spaces = new(@"\s+", RegexOptions.Compiled);
        private static readonly TimeSpan WeekendGap = TimeSpan.FromDays(5);

        private readonly IEpisodeService _episodeService;
        private readonly ITagRepository _tagRepository;
        private readonly ICached<Tag> _tagCache;
        private readonly Logger _logger;

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

            var release = TitleMatchVocabulary.Normalize(parsedEpisodeInfo.ReleaseTitle);
            var episode = FindEpisode(series, release);

            if (episode == null)
            {
                return null;
            }

            _logger.Debug("Matched '{0}' to {1}x{2:00} '{3}' by title", parsedEpisodeInfo.ReleaseTitle, episode.SeasonNumber, episode.EpisodeNumber, episode.Title);

            var eventEpisodeInfo = parsedEpisodeInfo.JsonClone();
            eventEpisodeInfo.SeasonNumbers = [episode.SeasonNumber];
            eventEpisodeInfo.EpisodeNumbers = [episode.EpisodeNumber];
            eventEpisodeInfo.AbsoluteEpisodeNumbers = [];
            eventEpisodeInfo.SpecialAbsoluteEpisodeNumbers = [];
            eventEpisodeInfo.AirDate = null;
            eventEpisodeInfo.DailyPart = null;
            eventEpisodeInfo.FullSeason = false;
            eventEpisodeInfo.IsPartialSeason = false;
            eventEpisodeInfo.IsSeasonExtra = false;
            eventEpisodeInfo.IsSeasonTitle = false;
            eventEpisodeInfo.IsSplitEpisode = false;
            eventEpisodeInfo.IsMiniSeries = false;
            eventEpisodeInfo.SeasonPart = 0;
            eventEpisodeInfo.Special = false;
            eventEpisodeInfo.Languages = WithoutEventLanguages(parsedEpisodeInfo.Languages, release);

            return eventEpisodeInfo;
        }

        private Episode FindEpisode(Series series, string release)
        {
            if (!IsSingleEvent(release, TitleMatchVocabulary.Normalize(series.Title)))
            {
                return null;
            }

            var releaseSession = ParseSession(release, out _, out var sessionCount);

            if (sessionCount > 1)
            {
                return null;
            }

            var weekends = FindWeekends(series, release, out var round);
            var matchedWeekends = weekends.Where(w => w.Names.Any(n => TitleMatchVocabulary.ContainsPhrase(release, n))).ToList();

            if (round.HasValue)
            {
                if (round.Value < 1 || round.Value > weekends.Count)
                {
                    return null;
                }

                var roundWeekend = weekends[round.Value - 1];

                if (matchedWeekends.Any() && !matchedWeekends.Contains(roundWeekend))
                {
                    return null;
                }

                matchedWeekends = new List<Weekend> { roundWeekend };
            }

            if (matchedWeekends.Count != 1)
            {
                return null;
            }

            var weekend = matchedWeekends.Single();
            var matches = weekend.Events.Where(e => SessionMatches(releaseSession, e, weekend)).ToList();

            if (matches.Count != 1)
            {
                return null;
            }

            var episode = matches.Single().Episode;

            // The release can't exist before the event; a future match means the name fits another year.
            if (!episode.AirDateUtc.HasValue || episode.AirDateUtc.Value > DateTime.UtcNow.AddDays(1))
            {
                return null;
            }

            return episode;
        }

        // "Italian" in "Italian Grand Prix" names the event, not the audio.
        private static List<Language> WithoutEventLanguages(List<Language> languages, string release)
        {
            var eventLanguages = TitleMatchVocabulary.Places
                .SelectMany(p => p)
                .Where(n => TitleMatchVocabulary.ContainsPhrase(release, n))
                .SelectMany(LanguageParser.ParseLanguages)
                .Where(l => l != Language.Unknown)
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

            var tag = _tagCache.Get(TagLabel, () => _tagRepository.FindByLabel(TagLabel), TimeSpan.FromMinutes(5));

            return tag != null && series.Tags.Contains(tag.Id);
        }

        private static bool IsSingleEvent(string release, string seriesTitle)
        {
            if (TitleMatchVocabulary.Testing.IsMatch(release) ||
                TitleMatchVocabulary.NonEventWords.Any(w => TitleMatchVocabulary.ContainsPhrase(release, w)))
            {
                return false;
            }

            return !TitleMatchVocabulary.SiblingSeriesWords.Any(w => TitleMatchVocabulary.ContainsPhrase(release, w) &&
                                                                     !TitleMatchVocabulary.ContainsPhrase(seriesTitle, w));
        }

        private List<Weekend> FindWeekends(Series series, string release, out int? round)
        {
            var roundMatch = RoundRegex.Match(release);
            round = roundMatch.Success ? int.Parse(roundMatch.Groups["round"].Value) : null;

            foreach (Match yearMatch in YearRegex.Matches(release))
            {
                var episodes = _episodeService.GetEpisodesBySeason(series.Id, int.Parse(yearMatch.Groups["year"].Value));

                if (episodes.Empty())
                {
                    continue;
                }

                if (yearMatch.Groups["round"].Success)
                {
                    round ??= int.Parse(yearMatch.Groups["round"].Value);
                }

                return GroupWeekends(episodes);
            }

            return new List<Weekend>();
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
                }
            }

            return weekends.OrderBy(w => w.Events[0].AirDate).ToList();
        }

        private static TitleEvent ParseEvent(Episode episode)
        {
            if (episode.Title.IsNullOrWhiteSpace())
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

            return new TitleEvent(episode, session, PlaceNames(place));
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
            var practices = weekend.Events.Where(e => e.Session != null && e.Session.StartsWith("practice")).ToList();

            return releaseSession.StartsWith("practice") && practices.Count == 1 && practices[0] == titleEvent;
        }

        private class TitleEvent
        {
            public TitleEvent(Episode episode, string session, string[] names)
            {
                Episode = episode;
                Session = session;
                Names = names;
                AirDate = episode.AirDateUtc ?? DateTime.MaxValue;
            }

            public Episode Episode { get; }
            public string Session { get; }
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
            public List<TitleEvent> Events { get; } = new();
        }
    }
}
