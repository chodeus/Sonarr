import React, { useCallback, useMemo, useState } from 'react';
import Alert from 'Components/Alert';
import FormInput from 'Components/Form/FormInput';
import FormInputHelpText from 'Components/Form/FormInputHelpText';
import FormLabel from 'Components/Form/FormLabel';
import FormRow from 'Components/Form/FormRow';
import Button from 'Components/Link/Button';
import LoadingIndicator from 'Components/Loading/LoadingIndicator';
import { inputTypes, kinds, sizes } from 'Helpers/Props';
import { SeriesType } from 'Series/Series';
import { InputChanged } from 'typings/inputs';
import { ApiError } from 'Utilities/Fetch/fetchJson';
import translate from 'Utilities/String/translate';
import EpisodeOrderPreviewModal from './EpisodeOrderPreviewModal';
import {
  canChangeEpisodeOrder,
  EpisodeOrderPreview,
} from './useEpisodeOrderPreview';
import styles from './EpisodeOrderChange.module.css';

interface EpisodeOrderChangeProps {
  seriesPath: string;
  seriesType: SeriesType;
  preview?: EpisodeOrderPreview;
  isFetching: boolean;
  error: ApiError | null;
  renameFiles: boolean;
  onRenameFilesChange: (change: InputChanged<boolean>) => void;
}

function EpisodeOrderChange({
  seriesPath,
  seriesType,
  preview,
  isFetching,
  error,
  renameFiles,
  onRenameFilesChange,
}: EpisodeOrderChangeProps) {
  const [isPreviewModalOpen, setIsPreviewModalOpen] = useState(false);

  const counts = useMemo(() => {
    const files = preview?.files ?? [];

    return {
      renamed: files.filter((f) => f.status === 'renamed').length,
      unchanged: files.filter((f) => f.status === 'unchanged').length,
      conflict: files.filter((f) => f.status === 'conflict').length,
      unmatched: files.filter((f) => f.status === 'unmatched').length,
    };
  }, [preview]);

  const handleShowChangesPress = useCallback(() => {
    setIsPreviewModalOpen(true);
  }, []);

  const handlePreviewModalClose = useCallback(() => {
    setIsPreviewModalOpen(false);
  }, []);

  if (isFetching) {
    return <LoadingIndicator className={styles.loading} size={20} />;
  }

  if (error || !preview) {
    return (
      <Alert kind={kinds.DANGER}>
        {translate('EpisodeOrderPreviewLoadError')}
      </Alert>
    );
  }

  const canChange = canChangeEpisodeOrder(preview);
  const hasChanges = counts.renamed + counts.conflict + counts.unmatched > 0;

  return (
    <>
      {preview.refreshRequired ? (
        <Alert kind={kinds.WARNING}>
          {translate('EpisodeOrderRefreshRequired')}
        </Alert>
      ) : null}

      {!preview.refreshRequired && counts.unmatched > 0 ? (
        <Alert kind={kinds.DANGER}>
          {translate('EpisodeOrderUnmatchedFiles', {
            count: counts.unmatched,
          })}
        </Alert>
      ) : null}

      {canChange ? (
        <Alert kind={kinds.INFO}>
          <div>
            {translate('EpisodeOrderChangeSummary', {
              renamed: counts.renamed,
              unchanged: counts.unchanged,
            })}
          </div>

          {preview.episodesAdded > 0 || preview.episodesRemoved > 0 ? (
            <div>
              {translate('EpisodeOrderEpisodeChanges', {
                added: preview.episodesAdded,
                removed: preview.episodesRemoved,
              })}
            </div>
          ) : null}

          {counts.conflict > 0 ? (
            <div>
              {translate('EpisodeOrderConflicts', { count: counts.conflict })}
            </div>
          ) : null}
        </Alert>
      ) : null}

      {seriesType === 'anime' && !preview.hasAbsoluteEpisodeNumbers ? (
        <Alert kind={kinds.WARNING}>
          {translate('EpisodeOrderNoAbsoluteNumbers')}
        </Alert>
      ) : null}

      {hasChanges ? (
        <div className={styles.showChanges}>
          <Button onPress={handleShowChangesPress}>
            {translate('EpisodeOrderShowChanges')}
          </Button>
        </div>
      ) : null}

      {canChange && preview.files.length ? (
        <FormRow size={sizes.MEDIUM}>
          <FormLabel>{translate('EpisodeOrderRenameFiles')}</FormLabel>

          <FormInputHelpText
            text={translate('EpisodeOrderRenameFilesHelpText')}
          />
          <FormInput
            type={inputTypes.CHECK}
            name="renameFiles"
            value={renameFiles}
            onChange={onRenameFilesChange}
          />
        </FormRow>
      ) : null}

      <EpisodeOrderPreviewModal
        isOpen={isPreviewModalOpen}
        seriesPath={seriesPath}
        preview={preview}
        onModalClose={handlePreviewModalClose}
      />
    </>
  );
}

export default EpisodeOrderChange;
