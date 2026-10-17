import React from 'react';
import Modal from 'Components/Modal/Modal';
import { sizes } from 'Helpers/Props';
import EpisodeOrderPreviewModalContent, {
  EpisodeOrderPreviewModalContentProps,
} from './EpisodeOrderPreviewModalContent';

interface EpisodeOrderPreviewModalProps
  extends EpisodeOrderPreviewModalContentProps {
  isOpen: boolean;
}

function EpisodeOrderPreviewModal({
  isOpen,
  onModalClose,
  ...otherProps
}: EpisodeOrderPreviewModalProps) {
  return (
    <Modal
      isOpen={isOpen}
      size={sizes.EXTRA_EXTRA_LARGE}
      onModalClose={onModalClose}
    >
      {isOpen ? (
        <EpisodeOrderPreviewModalContent
          {...otherProps}
          onModalClose={onModalClose}
        />
      ) : null}
    </Modal>
  );
}

export default EpisodeOrderPreviewModal;
