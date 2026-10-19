import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import SeriesMonitorNewItemsOptionsPopoverContent from 'AddSeries/SeriesMonitorNewItemsOptionsPopoverContent';
import CommandNames from 'Commands/CommandNames';
import Form from 'Components/Form/Form';
import FormInput from 'Components/Form/FormInput';
import FormInputButton from 'Components/Form/FormInputButton';
import FormInputHelpText from 'Components/Form/FormInputHelpText';
import FormLabel from 'Components/Form/FormLabel';
import FormRow from 'Components/Form/FormRow';
import Icon from 'Components/Icon';
import Button from 'Components/Link/Button';
import SpinnerErrorButton from 'Components/Link/SpinnerErrorButton';
import ModalBody from 'Components/Modal/ModalBody';
import ModalContent from 'Components/Modal/ModalContent';
import ModalFooter from 'Components/Modal/ModalFooter';
import ModalHeader from 'Components/Modal/ModalHeader';
import Popover from 'Components/Tooltip/Popover';
import { usePendingChangesStore } from 'Helpers/Hooks/usePendingChangesStore';
import usePrevious from 'Helpers/Hooks/usePrevious';
import {
  icons,
  inputTypes,
  kinds,
  sizes,
  tooltipPositions,
} from 'Helpers/Props';
import MoveSeriesModal from 'Series/MoveSeries/MoveSeriesModal';
import Series from 'Series/Series';
import SeriesMovePending from 'Series/SeriesMovePending';
import { useSaveSeries, useSingleSeries } from 'Series/useSeries';
import { InputChanged } from 'typings/inputs';
import selectSettings from 'Utilities/selectSettings';
import translate from 'Utilities/String/translate';
import EpisodeOrderChange from './EpisodeOrder/EpisodeOrderChange';
import useChangeEpisodeOrder from './EpisodeOrder/useChangeEpisodeOrder';
import useEpisodeOrderPreview from './EpisodeOrder/useEpisodeOrderPreview';
import RootFolderModal from './RootFolder/RootFolderModal';
import { RootFolderUpdated } from './RootFolder/RootFolderModalContent';
import styles from './EditSeriesModalContent.module.css';

export interface EditSeriesModalContentProps {
  seriesId: number;
  onModalClose: () => void;
  onDeleteSeriesPress: () => void;
}

function EditSeriesModalContent({
  seriesId,
  onModalClose,
  onDeleteSeriesPress,
}: EditSeriesModalContentProps) {
  const series = useSingleSeries(seriesId)!;

  const {
    title,
    monitored,
    monitorNewItems,
    seasonFolder,
    qualityProfileId,
    seriesType,
    seasonType,
    seasonTypes,
    language,
    path,
    pendingPath,
    tags,
    rootFolderPath: initialRootFolderPath,
  } = series;

  const { pendingChanges, setPendingChange } = usePendingChangesStore<Series>(
    {}
  );

  const [isRootFolderModalOpen, setIsRootFolderModalOpen] = useState(false);
  const [rootFolderPath, setRootFolderPath] = useState(initialRootFolderPath);
  const isPathChanging = !!(
    pendingChanges.path && path !== pendingChanges.path
  );
  const [isConfirmMoveModalOpen, setIsConfirmMoveModalOpen] = useState(false);

  const { saveSeries, isSaving, saveError } = useSaveSeries();
  const wasSaving = usePrevious(isSaving);
  const {
    changeEpisodeOrder,
    isChangingEpisodeOrder,
    changeEpisodeOrderError,
  } = useChangeEpisodeOrder();
  const approvedOrderChangeRef = useRef<{
    seasonType: string;
    renameFiles: boolean;
  } | null>(null);

  const pendingSeasonType = pendingChanges.seasonType ?? seasonType;
  const isSeasonTypeChanging = pendingSeasonType !== seasonType;
  const [renameFiles, setRenameFiles] = useState(true);

  const {
    data: episodeOrderPreview,
    isFetching: isEpisodeOrderPreviewFetching,
    error: episodeOrderPreviewError,
  } = useEpisodeOrderPreview(seriesId, pendingSeasonType, isSeasonTypeChanging);

  const canSave =
    !isSeasonTypeChanging ||
    (!isEpisodeOrderPreviewFetching &&
      !episodeOrderPreviewError &&
      !!episodeOrderPreview?.canChange);

  const { settings, ...otherSettings } = useMemo(() => {
    return selectSettings(
      {
        monitored,
        monitorNewItems,
        seasonFolder,
        qualityProfileId,
        seriesType,
        seasonType,
        language,
        path,
        tags,
      },
      pendingChanges,
      saveError
    );
  }, [
    monitored,
    monitorNewItems,
    seasonFolder,
    qualityProfileId,
    seriesType,
    seasonType,
    language,
    path,
    tags,
    pendingChanges,
    saveError,
  ]);

  const handleInputChange = useCallback(
    ({ name, value }: InputChanged) => {
      // @ts-expect-error name needs to be keyof Series
      setPendingChange(name, value);
    },
    [setPendingChange]
  );

  const handleRenameFilesChange = useCallback(
    ({ value }: InputChanged<boolean>) => {
      setRenameFiles(value);
    },
    []
  );

  const handleRootFolderPress = useCallback(() => {
    setIsRootFolderModalOpen(true);
  }, []);

  const handleRootFolderModalClose = useCallback(() => {
    setIsRootFolderModalOpen(false);
  }, []);

  const handleRootFolderChange = useCallback(
    ({
      path: newPath,
      rootFolderPath: newRootFolderPath,
    }: RootFolderUpdated) => {
      setIsRootFolderModalOpen(false);
      setRootFolderPath(newRootFolderPath);
      handleInputChange({ name: 'path', value: newPath });
    },
    [handleInputChange]
  );

  const handleCancelPress = useCallback(() => {
    setIsConfirmMoveModalOpen(false);
  }, []);

  // Files move only when the user confirmed it in the move modal
  const startSave = useCallback(
    (moveFiles = false) => {
      // The order sent after the save is the one previewed when Save was pressed; the PUT keeps the stored order
      approvedOrderChangeRef.current = isSeasonTypeChanging
        ? { seasonType: pendingSeasonType, renameFiles }
        : null;

      saveSeries(
        {
          ...series,
          ...pendingChanges,
          seasonType,
        },
        moveFiles
      );
    },
    [
      series,
      seasonType,
      pendingChanges,
      isSeasonTypeChanging,
      pendingSeasonType,
      renameFiles,
      saveSeries,
    ]
  );

  const handleSavePress = useCallback(() => {
    if (isPathChanging && !isConfirmMoveModalOpen) {
      setIsConfirmMoveModalOpen(true);
    } else {
      setIsConfirmMoveModalOpen(false);
      startSave();
    }
  }, [isPathChanging, isConfirmMoveModalOpen, startSave]);

  const handleMoveSeriesPress = useCallback(() => {
    setIsConfirmMoveModalOpen(false);
    startSave(true);
  }, [startSave]);

  useEffect(() => {
    if (!isSaving && wasSaving && !saveError) {
      const orderChange = approvedOrderChangeRef.current;

      if (orderChange) {
        changeEpisodeOrder(
          { name: CommandNames.ChangeEpisodeOrder, seriesId, ...orderChange },
          { onSuccess: onModalClose }
        );
      } else {
        onModalClose();
      }
    }
  }, [
    seriesId,
    isSaving,
    wasSaving,
    saveError,
    changeEpisodeOrder,
    onModalClose,
  ]);

  return (
    <ModalContent onModalClose={onModalClose}>
      <ModalHeader>{translate('EditSeriesModalHeader', { title })}</ModalHeader>

      <ModalBody>
        <Form {...otherSettings}>
          <FormRow size={sizes.MEDIUM}>
            <FormLabel>{translate('Monitored')}</FormLabel>

            <FormInputHelpText text={translate('MonitoredEpisodesHelpText')} />
            <FormInput
              type={inputTypes.CHECK}
              name="monitored"
              {...settings.monitored}
              onChange={handleInputChange}
            />
          </FormRow>

          <FormRow size={sizes.MEDIUM}>
            <FormLabel>
              {translate('MonitorNewSeasons')}
              <Popover
                anchor={<Icon className={styles.labelIcon} name={icons.INFO} />}
                title={translate('MonitorNewSeasons')}
                body={<SeriesMonitorNewItemsOptionsPopoverContent />}
                position={tooltipPositions.RIGHT}
              />
            </FormLabel>

            <FormInputHelpText text={translate('MonitorNewSeasonsHelpText')} />
            <FormInput
              type={inputTypes.MONITOR_NEW_ITEMS_SELECT}
              name="monitorNewItems"
              {...settings.monitorNewItems}
              onChange={handleInputChange}
            />
          </FormRow>

          <FormRow size={sizes.MEDIUM}>
            <FormLabel>{translate('UseSeasonFolder')}</FormLabel>

            <FormInputHelpText text={translate('UseSeasonFolderHelpText')} />
            <FormInput
              type={inputTypes.CHECK}
              name="seasonFolder"
              {...settings.seasonFolder}
              onChange={handleInputChange}
            />
          </FormRow>

          <FormRow size={sizes.MEDIUM}>
            <FormLabel>{translate('QualityProfile')}</FormLabel>

            <FormInput
              type={inputTypes.QUALITY_PROFILE_SELECT}
              name="qualityProfileId"
              {...settings.qualityProfileId}
              onChange={handleInputChange}
            />
          </FormRow>

          <FormRow size={sizes.MEDIUM}>
            <FormLabel>{translate('SeriesType')}</FormLabel>

            <FormInputHelpText text={translate('SeriesTypesHelpText')} />
            <FormInput
              type={inputTypes.SERIES_TYPE_SELECT}
              name="seriesType"
              {...settings.seriesType}
              onChange={handleInputChange}
            />
          </FormRow>

          <FormRow size={sizes.MEDIUM}>
            <FormLabel>{translate('Language')}</FormLabel>

            <FormInputHelpText text={translate('SeriesLanguageHelpText')} />
            <FormInput
              type={inputTypes.LANGUAGE_SELECT}
              name="language"
              {...settings.language}
              onChange={handleInputChange}
            />
          </FormRow>

          <FormRow size={sizes.MEDIUM}>
            <FormLabel>{translate('SeasonType')}</FormLabel>

            <FormInputHelpText text={translate('SeasonTypeHelpText')} />
            <FormInput
              type={inputTypes.SEASON_TYPE_SELECT}
              name="seasonType"
              {...settings.seasonType}
              seasonTypes={seasonTypes}
              isDisabled={!seasonTypes.length}
              includeNoChange={false}
              includeMixed={false}
              onChange={handleInputChange}
            />
          </FormRow>

          {isSeasonTypeChanging ? (
            <EpisodeOrderChange
              seriesPath={path}
              seriesType={settings.seriesType.value}
              preview={episodeOrderPreview}
              isFetching={isEpisodeOrderPreviewFetching}
              error={episodeOrderPreviewError}
              renameFiles={renameFiles}
              onRenameFilesChange={handleRenameFilesChange}
            />
          ) : null}

          <FormRow size={sizes.MEDIUM}>
            <FormLabel>
              {translate('Path')}

              {pendingPath ? (
                <SeriesMovePending
                  className={styles.labelIcon}
                  pendingPath={pendingPath}
                />
              ) : null}
            </FormLabel>

            <FormInput
              type={inputTypes.PATH}
              name="path"
              {...settings.path}
              buttons={[
                <FormInputButton
                  key="fileBrowser"
                  kind={kinds.DEFAULT}
                  title={translate('RootFolder')}
                  onPress={handleRootFolderPress}
                >
                  <Icon name={icons.ROOT_FOLDER} />
                </FormInputButton>,
              ]}
              includeFiles={false}
              onChange={handleInputChange}
            />
          </FormRow>

          <FormRow size={sizes.MEDIUM}>
            <FormLabel>{translate('Tags')}</FormLabel>

            <FormInput
              type={inputTypes.TAG}
              name="tags"
              {...settings.tags}
              onChange={handleInputChange}
            />
          </FormRow>
        </Form>
      </ModalBody>

      <ModalFooter>
        <Button
          className={styles.deleteButton}
          kind={kinds.DANGER}
          onPress={onDeleteSeriesPress}
        >
          {translate('Delete')}
        </Button>

        <Button onPress={onModalClose}>{translate('Cancel')}</Button>

        <SpinnerErrorButton
          error={saveError ?? changeEpisodeOrderError}
          isSpinning={isSaving || isChangingEpisodeOrder}
          isDisabled={!canSave}
          onPress={handleSavePress}
        >
          {translate('Save')}
        </SpinnerErrorButton>
      </ModalFooter>

      <RootFolderModal
        isOpen={isRootFolderModalOpen}
        seriesId={seriesId}
        languageId={settings.language.value.id}
        rootFolderPath={rootFolderPath}
        onSavePress={handleRootFolderChange}
        onModalClose={handleRootFolderModalClose}
      />

      <MoveSeriesModal
        originalPath={path}
        destinationPath={pendingChanges.path}
        isOpen={isConfirmMoveModalOpen}
        onModalClose={handleCancelPress}
        onSavePress={handleSavePress}
        onMoveSeriesPress={handleMoveSeriesPress}
      />
    </ModalContent>
  );
}

export default EditSeriesModalContent;
