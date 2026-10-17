import React, { useMemo } from 'react';
import Alert from 'Components/Alert';
import Button from 'Components/Link/Button';
import InlineMarkdown from 'Components/Markdown/InlineMarkdown';
import ModalBody from 'Components/Modal/ModalBody';
import ModalContent from 'Components/Modal/ModalContent';
import ModalFooter from 'Components/Modal/ModalFooter';
import ModalHeader from 'Components/Modal/ModalHeader';
import translate from 'Utilities/String/translate';
import EpisodeOrderPreviewRow from './EpisodeOrderPreviewRow';
import {
  EpisodeOrderPreview,
  EpisodeOrderPreviewFileStatus,
} from './useEpisodeOrderPreview';
import styles from './EpisodeOrderPreviewModalContent.module.css';

const STATUS_ORDER: EpisodeOrderPreviewFileStatus[] = [
  'unmatched',
  'unnameable',
  'conflict',
  'renamed',
];

export interface EpisodeOrderPreviewModalContentProps {
  seriesPath: string;
  preview: EpisodeOrderPreview;
  onModalClose: () => void;
}

function EpisodeOrderPreviewModalContent({
  seriesPath,
  preview,
  onModalClose,
}: EpisodeOrderPreviewModalContentProps) {
  const files = useMemo(() => {
    return STATUS_ORDER.flatMap((status) =>
      preview.files.filter((file) => file.status === status)
    );
  }, [preview]);

  return (
    <ModalContent onModalClose={onModalClose}>
      <ModalHeader>{translate('EpisodeOrderPreviewModalHeader')}</ModalHeader>

      <ModalBody>
        <Alert>
          <InlineMarkdown
            data={translate('OrganizeRelativePaths', { path: seriesPath })}
            blockClassName={styles.path}
          />
        </Alert>

        {files.length ? (
          <div className={styles.previews}>
            {files.map(({ episodeFileId, ...file }) => {
              return <EpisodeOrderPreviewRow key={episodeFileId} {...file} />;
            })}
          </div>
        ) : (
          <div>{translate('EpisodeOrderNoFileChanges')}</div>
        )}
      </ModalBody>

      <ModalFooter>
        <Button onPress={onModalClose}>{translate('Close')}</Button>
      </ModalFooter>
    </ModalContent>
  );
}

export default EpisodeOrderPreviewModalContent;
