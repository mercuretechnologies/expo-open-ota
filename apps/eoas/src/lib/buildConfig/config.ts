import fs from 'fs-extra';
import path from 'path';

import { CONFIG_VALIDATION_OPTIONS, XpremConfigSchema } from './schema';
import { XpremConfig } from './types';

export const CONFIG_FILENAME = 'xprem.json';
export type ConfigIssue = { path: string; message: string };
export function validateConfig(input: unknown): ConfigIssue[] {
  const { error } = XpremConfigSchema.validate(input, CONFIG_VALIDATION_OPTIONS);
  return (error?.details ?? []).map(detail => ({
    path: detail.path.join('.') || '$',
    message: detail.type === 'string.pattern.base' ? 'Invalid format' : detail.message,
  }));
}

export function parseConfig(input: unknown): XpremConfig {
  const issues = validateConfig(input);
  if (issues.length) {
    throw new Error(issues.map(issue => `${issue.path}: ${issue.message}`).join('\n'));
  }
  return input as XpremConfig;
}

export async function readJsonFile(file: string): Promise<unknown> {
  const stat = await fs.stat(file);
  if (!stat.isFile() || stat.size > 1024 * 1024) {
    throw new Error(`${path.basename(file)} must be a JSON file smaller than 1 MiB.`);
  }
  const content = await fs.readFile(file, 'utf8');
  try {
    return JSON.parse(content);
  } catch {
    // JSON.parse errors can include values from the input, including secrets.
    throw new Error(
      `Invalid JSON in ${path.basename(
        file
      )}. Check syntax; comments and trailing commas are not supported.`
    );
  }
}

export async function readConfig(file: string): Promise<XpremConfig> {
  let input: unknown;
  try {
    input = await readJsonFile(file);
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') {
      throw new Error(`${CONFIG_FILENAME} was not found. Run "eoas build:configure" to create it.`);
    }
    throw error;
  }
  try {
    return parseConfig(input);
  } catch (error) {
    throw new Error(`Invalid ${path.basename(file)}:\n${(error as Error).message}`);
  }
}

export async function writeNewConfig(file: string, input: unknown): Promise<void> {
  const config = parseConfig(input);
  await fs.writeFile(file, `${JSON.stringify(config, null, 2)}\n`, { flag: 'wx' });
}
