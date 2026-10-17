import React from 'react';
import Icon from 'Components/Icon';
import Label from 'Components/Label';
import { icons, kinds } from 'Helpers/Props';
import padNumber from 'Utilities/Number/padNumber';
import translate from 'Utilities/String/translate';
import {
  EpisodeOrderPreviewEpisode,
  EpisodeOrderPreviewFile,
} from './useEpisodeOrderPreview';
import styles from './EpisodeOrderPreviewRow.module.css';

function formatEpisodes(episodes: EpisodeOrderPreviewEpisode[]) {
  return episodes
    .map(
      ({ seasonNumber, episodeNumber }) =>
        `${seasonNumber}x${padNumber(episodeNumber, 2)}`
    )
    .join(', ');
}

type EpisodeOrderPreviewRowProps = Omit<
  EpisodeOrderPreviewFile,
  'episodeFileId'
>;

function EpisodeOrderPreviewRow({
  existingPath,
  newPath,
  existingEpisodes,
  newEpisodes,
  status,
}: EpisodeOrderPreviewRowProps) {
  return (
    <div className={styles.row}>
      <div className={styles.episodes}>
        {formatEpisodes(existingEpisodes)}
        {newEpisodes.length ? ` → ${formatEpisodes(newEpisodes)}` : null}

        {status === 'conflict' ? (
          <Label className={styles.status} kind={kinds.WARNING}>
            {translate('EpisodeOrderDestinationInUse')}
          </Label>
        ) : null}

        {status === 'unnameable' ? (
          <Label className={styles.status} kind={kinds.DANGER}>
            {translate('EpisodeOrderCannotName')}
          </Label>
        ) : null}

        {status === 'unmatched' ? (
          <Label className={styles.status} kind={kinds.DANGER}>
            {translate('EpisodeOrderNotInOrder')}
          </Label>
        ) : null}
      </div>

      <div>
        <Icon name={icons.SUBTRACT} kind={kinds.DANGER} />

        <span className={styles.path}>{existingPath}</span>
      </div>

      {newPath && newPath !== existingPath ? (
        <div>
          <Icon name={icons.ADD} kind={kinds.SUCCESS} />

          <span className={styles.path}>{newPath}</span>
        </div>
      ) : null}
    </div>
  );
}

export default EpisodeOrderPreviewRow;
