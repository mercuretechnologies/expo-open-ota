import { Command } from '@oclif/core';
import fs from 'fs-extra';
import path from 'path';

import {
  createOrModifyExpoConfigAsync,
  getExpoConfigUpdateUrl,
  getPrivateExpoConfigAsync,
} from '../../lib/expoConfig';
import Log from '../../lib/log';
import { ora } from '../../lib/ora';
import { isExpoInstalled } from '../../lib/package';
import { confirmAsync, promptAsync } from '../../lib/prompts';
import { ensurePrivateKeyIgnored, isValidUpdateUrl } from '../../lib/utils';

export default class UpdateConfigure extends Command {
  static override description = 'Configure an existing Expo project to use xprem updates';
  static override examples = ['<%= config.bin %> <%= command.id %>'];

  public async run(): Promise<void> {
    const projectDir = process.cwd();
    if (!isExpoInstalled(projectDir)) {
      Log.error('Expo is not installed in this project. Please install Expo first.');
      return;
    }
    const config = await getPrivateExpoConfigAsync(projectDir);
    if (!config) {
      Log.error(
        'Could not find Expo config in this project. Please make sure you have an Expo config.'
      );
      return;
    }
    const { appId } = await promptAsync({
      message:
        'Enter the xprem application ID (sent as the expo-app-id header).\n' +
        '  See https://mercure-technologies.gitbook.io/xprem/stateless-mode/getting-started for details.',
      name: 'appId',
      type: 'text',
      validate: value => !!value,
    });
    const { updateUrl: promptedUrl } = await promptAsync({
      message:
        'Enter the URL of your update server (ex: https://customota.com or https://api.example.com/ota)',
      name: 'updateUrl',
      type: 'text',
      initial: (getExpoConfigUpdateUrl(config) || '').replace(/\/manifest$/, ''),
      validate: value => !!value && isValidUpdateUrl(value),
    });
    let manifestEndpoint = `${promptedUrl.replace(/\/+$/, '')}/manifest`;
    const updateUrl = getExpoConfigUpdateUrl(config);
    if (updateUrl && !updateUrl.includes('expo.dev')) {
      const replace = await confirmAsync({
        message: `Expo config already has an update URL set to ${updateUrl}. Do you want to replace it?`,
        name: 'replace',
        type: 'confirm',
      });
      if (!replace) {
        manifestEndpoint = updateUrl;
      }
    }
    const hasCertificates = await confirmAsync({
      message: 'Do you have already generated your certificates for code signing?',
      name: 'certificates',
      type: 'confirm',
    });
    if (!hasCertificates) {
      Log.fail('You need to generate your certificates first by using npx eoas generate-certs');
      return;
    }
    const { codeSigningCertificatePath } = await promptAsync({
      message: 'Enter the path to your code signing certificate (ex: ./certs/certificate.pem)',
      name: 'codeSigningCertificatePath',
      type: 'text',
      initial: './certs/certificate.pem',
      validate: value => {
        try {
          const fullPath = path.resolve(projectDir, value);
          // eslint-disable-next-line node/no-sync
          if (!fs.existsSync(fullPath)) {
            Log.newLine();
            Log.error('File does not exist');
            return false;
          }
          // eslint-disable-next-line node/no-sync
          if (!fs.readFileSync(fullPath, 'utf8')) {
            Log.error('Empty key');
            return false;
          }
          return true;
        } catch {
          return false;
        }
      },
    });
    const updates = {
      url: manifestEndpoint,
      codeSigningMetadata:
        "process.env.DISABLE_CODE_SIGNING ? undefined : { keyid: 'main', alg: 'rsa-v1_5-sha256' }",
      codeSigningCertificate: `process.env.DISABLE_CODE_SIGNING ? undefined : '${codeSigningCertificatePath
        .replace(/\\/g, '\\\\')
        .replace(/'/g, "\\'")}'`,
      enabled: true,
      requestHeaders: {
        'expo-channel-name': {
          __comment: 'Declare as a literal if you surf branches: see xprem-branch below.',
          value: 'process.env.RELEASE_CHANNEL',
        },
        'expo-app-id': appId,
        'xprem-branch': {
          __comment: 'Branch surfing — the branch to serve; empty means the channel decides.',
          value: '',
        },
      },
    };
    ensurePrivateKeyIgnored(projectDir);
    const spinner = ora('Updating Expo config').start();
    try {
      await createOrModifyExpoConfigAsync(projectDir, { updates });
      spinner.succeed(
        'Expo config successfully updated do not forget to format the file with prettier or eslint'
      );
    } catch (error) {
      spinner.fail('Failed to update Expo config');
      throw error;
    }
  }
}
