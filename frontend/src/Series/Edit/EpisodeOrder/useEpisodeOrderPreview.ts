import useApiQuery from 'Helpers/Hooks/useApiQuery';

export type EpisodeOrderPreviewFileStatus =
  | 'unchanged'
  | 'renamed'
  | 'conflict'
  | 'unmatched'
  | 'unnameable';

export interface EpisodeOrderPreviewEpisode {
  seasonNumber: number;
  episodeNumber: number;
}

export interface EpisodeOrderPreviewFile {
  episodeFileId: number;
  existingPath: string;
  newPath: string | null;
  existingEpisodes: EpisodeOrderPreviewEpisode[];
  newEpisodes: EpisodeOrderPreviewEpisode[];
  status: EpisodeOrderPreviewFileStatus;
}

export interface EpisodeOrderPreview {
  seriesId: number;
  seasonType: string;
  canChange: boolean;
  refreshRequired: boolean;
  sceneNumberedEpisodes: number;
  sceneSeasonTitles: number;
  hasAbsoluteEpisodeNumbers: boolean;
  episodesAdded: number;
  episodesRemoved: number;
  files: EpisodeOrderPreviewFile[];
}

const useEpisodeOrderPreview = (
  seriesId: number,
  seasonType: string,
  isEnabled: boolean
) => {
  return useApiQuery<EpisodeOrderPreview>({
    path: `/series/${seriesId}/episodeorder`,
    queryParams: { seasonType },
    queryOptions: {
      enabled: isEnabled,
    },
  });
};

export default useEpisodeOrderPreview;
