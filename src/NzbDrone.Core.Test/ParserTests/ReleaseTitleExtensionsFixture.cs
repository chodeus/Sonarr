using FluentAssertions;
using NUnit.Framework;
using NzbDrone.Core.Parser;
using NzbDrone.Core.Test.Framework;

namespace NzbDrone.Core.Test.ParserTests
{
    [TestFixture]
    public class ReleaseTitleExtensionsFixture : CoreTest
    {
        [TestCase("Series.Title.S01E05.Episode.Title.1080p.AMZN.WEB-DL.DDPA5.1.H.264-Group", "Series Title S01E05 Episode Title 1080p AMZN WEB-DL DDPA5 1 H 264-Group")]
        [TestCase("Series.Title.S01E05.1080p.WEB-DL.AAC2.0.H.264-Group", "Series.Title.S01E05.1080p.WEB-DL.AAC2.0.H.264-Group.mkv")]
        [TestCase("Series Title S01E05 1080p WEB-DL DDP 5.1 Atmos H.264-Group", "series_title_s01e05_1080p_web-dl_ddp_5_1_atmos_h_264-group")]
        [TestCase("Series.Title.S01E05.2160p.WEB-DL.DV.HDR10+.H.265-Group", "Series.Title.S01E05.2160p.WEB-DL.DV.HDR10Plus.H.265-Group")]
        public void should_treat_titles_that_differ_only_in_case_separators_or_extension_as_the_same(string title, string otherTitle)
        {
            title.IsSameReleaseTitle(otherTitle).Should().BeTrue();
        }

        [TestCase("Series.Title.S01E05.1080p.AMZN.WEB-DL.DDPA5.1.H.264-Group", "Series.Title.S01E05.1080p.AMZN.WEB-DL.DDP5.1.H.264-Group")]
        [TestCase("Series.Title.S01E05.1080p.WEB-DL.AAC2.0.H.264-Group", "Series.Title.S01E05.REPACK.1080p.WEB-DL.AAC2.0.H.264-Group")]
        [TestCase("Series.Title.S01E05.2160p.WEB-DL.DV.HDR10.H.265-Group", "Series.Title.S01E05.2160p.WEB-DL.DV.HDR10+.H.265-Group")]
        [TestCase("Series.Title.S01E05.1080p.WEB-DL.DD2.0.H.264-Group", "Series.Title.S01E05.1080p.WEB-DL.DD+2.0.H.264-Group")]
        [TestCase("Series.and.Title.S01E05.1080p.WEB-DL.H.264-Group", "Series.Title.S01E05.1080p.WEB-DL.H.264-Group")]
        public void should_treat_titles_with_different_tokens_as_different(string title, string otherTitle)
        {
            title.IsSameReleaseTitle(otherTitle).Should().BeFalse();
        }

        [TestCase(null, null)]
        [TestCase("", "")]
        [TestCase("   ", "   ")]
        [TestCase("---", "...")]
        public void should_not_match_titles_with_nothing_left_to_compare(string title, string otherTitle)
        {
            title.IsSameReleaseTitle(otherTitle).Should().BeFalse();
        }
    }
}
