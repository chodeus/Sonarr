import { useQueryClient } from '@tanstack/react-query';
import Command, { NewCommandBody } from 'Commands/Command';
import useApiMutation, {
  addOrUpdateQueryClientItem,
} from 'Helpers/Hooks/useApiMutation';

// Unlike useExecuteCommand, the caller learns whether the command was accepted
const useChangeEpisodeOrder = () => {
  const queryClient = useQueryClient();

  const { mutate, isPending, error } = useApiMutation<Command, NewCommandBody>({
    method: 'POST',
    path: '/command',
    mutationOptions: {
      onSuccess: (newCommand: Command) => {
        queryClient.setQueryData<Command[]>(['/command'], (oldCommands = []) =>
          addOrUpdateQueryClientItem(oldCommands, newCommand, 'id')
        );
      },
    },
  });

  return {
    changeEpisodeOrder: mutate,
    isChangingEpisodeOrder: isPending,
    changeEpisodeOrderError: error,
  };
};

export default useChangeEpisodeOrder;
