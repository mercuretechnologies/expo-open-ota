// This file is copied from eas-cli v16.3.1 (MIT, https://github.com/expo/eas-cli/tree/v16.3.1) to ensure consistent user experience across the CLI.
import chalk from 'chalk';

import Log from './log';
import { confirmAsync, promptAsync } from './prompts';
import { Client } from './vcs/vcs';

export async function commitPromptAsync(
  vcsClient: Client,
  {
    initialCommitMessage,
    commitAllFiles,
  }: {
    initialCommitMessage?: string;
    commitAllFiles?: boolean;
  } = {}
): Promise<void> {
  const { message } = await promptAsync({
    type: 'text',
    name: 'message',
    message: 'Commit message:',
    initial: initialCommitMessage,
    validate: (input: string) => input !== '',
  });
  await vcsClient.commitAsync({
    commitAllFiles,
    commitMessage: message,
    nonInteractive: false,
  });
}

export async function ensureRepoIsCleanAsync(
  vcsClient: Client,
  nonInteractive = false
): Promise<void> {
  if (!(await vcsClient.isCommitRequiredAsync())) {
    return;
  }
  Log.addNewLineIfNone();
  Log.warn(`${chalk.bold('Warning!')} Your repository working tree is dirty.`);
  Log.log(
    `This operation needs to be run on a clean working tree. ${chalk.bold(
      'Commit all your changes before proceeding'
    )}.`
  );
  if (nonInteractive) {
    Log.log('The following files need to be committed:');
    await vcsClient.showChangedFilesAsync();

    throw new Error('Commit all changes. Aborting...');
  }
  const answer = await confirmAsync({
    message: `Commit changes to git?`,
    type: 'confirm',
    name: 'confirm git commit',
  });
  if (answer) {
    await commitPromptAsync(vcsClient, { commitAllFiles: true });
  } else {
    throw new Error('Commit all changes. Aborting...');
  }
}
