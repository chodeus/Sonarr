using System.Text.RegularExpressions;
using NzbDrone.Common.Extensions;
using NzbDrone.Core.MediaFiles;

namespace NzbDrone.Core.Parser
{
    public static class ReleaseTitleExtensions
    {
        private static readonly Regex SeparatorRegex = new(@"[^\p{L}\p{N}]+", RegexOptions.Compiled);

        public static bool IsSameReleaseTitle(this string title, string otherTitle)
        {
            var cleanTitle = CleanReleaseTitle(title);

            return cleanTitle.IsNotNullOrWhiteSpace() && cleanTitle == CleanReleaseTitle(otherTitle);
        }

        // Only case and separators may differ: "+" carries meaning (HDR10+, DD+), so it can't be dropped like a dot
        private static string CleanReleaseTitle(string title)
        {
            if (title.IsNullOrWhiteSpace())
            {
                return null;
            }

            var withoutExtension = FileExtensions.RemoveFileExtension(title).Replace("+", "plus");

            return SeparatorRegex.Replace(withoutExtension, string.Empty).ToLowerInvariant();
        }
    }
}
