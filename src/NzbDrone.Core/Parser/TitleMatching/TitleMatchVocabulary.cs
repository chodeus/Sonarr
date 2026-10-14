using System.Collections.Generic;
using System.Text.RegularExpressions;
using NzbDrone.Common.Extensions;

namespace NzbDrone.Core.Parser.TitleMatching
{
    public static class TitleMatchVocabulary
    {
        // First entry is the name TheTVDB uses; the rest are what releases call the same place.
        public static readonly IReadOnlyList<string[]> Places = new List<string[]>
        {
            new[] { "abu dhabi", "abudhabi", "yas marina" },
            new[] { "australia", "australian", "melbourne", "albert park" },
            new[] { "austria", "austrian", "spielberg", "red bull ring" },
            new[] { "azerbaijan", "baku" },
            new[] { "bahrain", "sakhir" },
            new[] { "barcelona catalunya", "barcelona", "catalunya", "catalonia" },
            new[] { "belgium", "belgian", "spa francorchamps" },
            new[] { "brazil", "brazilian", "sao paulo", "saopaulo", "interlagos" },
            new[] { "canada", "canadian", "montreal" },
            new[] { "china", "chinese", "shanghai" },
            new[] { "emilia romagna", "imola" },
            new[] { "france", "french", "paul ricard", "magny cours" },
            new[] { "germany", "german", "hockenheim", "nurburgring" },
            new[] { "great britain", "greatbritain", "britain", "british", "silverstone", "united kingdom" },
            new[] { "hungary", "hungarian", "hungaroring", "budapest" },
            new[] { "italy", "italian", "monza" },
            new[] { "japan", "japanese", "suzuka" },
            new[] { "las vegas", "lasvegas", "vegas" },
            new[] { "mexico", "mexican", "mexico city", "mexicocity" },
            new[] { "miami" },
            new[] { "monaco", "monte carlo", "montecarlo", "monegasque" },
            new[] { "netherlands", "dutch", "zandvoort", "holland" },
            new[] { "portugal", "portuguese", "portimao" },
            new[] { "qatar", "lusail" },
            new[] { "saudi arabia", "saudiarabia", "saudi", "jeddah" },
            new[] { "singapore", "marina bay" },
            new[] { "spain", "spanish", "madrid" },
            new[] { "turkey", "turkish", "istanbul" },
            new[] { "united states", "unitedstates", "usa", "austin", "circuit of the americas", "cota" }
        };

        // Most specific first: each match is removed before the next pattern runs.
        public static readonly IReadOnlyList<(string Session, Regex Pattern)> Sessions = new List<(string, Regex)>
        {
            ("sprint qualifying", Pattern(@"\bsprint (?:qualifying|quali|shootout)\b|\bshootout\b")),
            ("sprint", Pattern(@"\bsprint(?: race)?\b")),
            ("qualifying", Pattern(@"\b(?:qualifying|quali)\b")),
            ("practice {0}", Pattern(@"\b(?:free )?practice (?<number>[1-3])\b|\bfp(?<number>[1-3])\b")),
            ("practice", Pattern(@"\b(?:free )?practice\b")),
            ("warm up", Pattern(@"\bwarm ?up\b")),
            ("race {0}", Pattern(@"\brace (?<number>[1-3])\b")),
            ("race", Pattern(@"\brace\b"))
        };

        // A release with no session words is the weekend's main event.
        public static readonly IReadOnlyList<string> MainSessions = new[] { "race" };

        public static readonly Regex Testing = Pattern(@"\b(?:testing|test|shakedown)\b");

        public static readonly IReadOnlyList<string> NonEventWords = new[]
        {
            "show", "press", "conference", "notebook", "build up", "buildup", "highlights", "onboard", "preview",
            "review", "analysis", "interview", "kids", "paddock", "debrief", "report", "full event", "full events",
            "full race events", "startline", "finishline", "ozetler"
        };

        public static readonly IReadOnlyList<string> SiblingSeriesWords = new[]
        {
            "academy", "f2", "f3", "formula 2", "formula2", "formula 3", "formula3", "formula e", "moto2", "moto3",
            "motoe", "xfinity", "truck series", "indy nxt", "supercup", "w series"
        };

        private static readonly Regex NonAlphanumeric = new(@"[^a-z0-9]+", RegexOptions.Compiled);
        private static readonly Regex NumberWords = new(@"\b(?<word>one|two|three)\b", RegexOptions.Compiled);

        private static readonly Dictionary<string, string> Numbers = new()
        {
            { "one", "1" },
            { "two", "2" },
            { "three", "3" }
        };

        public static string Normalize(string title)
        {
            var normalized = NonAlphanumeric.Replace(title.ToLowerInvariant().RemoveDiacritics(), " ").Trim();

            return NumberWords.Replace(normalized, m => Numbers[m.Groups["word"].Value]);
        }

        // Both arguments must already be normalized.
        public static bool ContainsPhrase(string normalizedTitle, string phrase)
        {
            return $" {normalizedTitle} ".Contains($" {phrase} ");
        }

        private static Regex Pattern(string pattern)
        {
            return new Regex(pattern, RegexOptions.Compiled);
        }
    }
}
