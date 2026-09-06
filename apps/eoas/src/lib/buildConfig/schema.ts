import Joi from 'joi';

import { XpremConfig } from './types';

export const ApplicationIdSchema = Joi.string()
  .max(255)
  .pattern(/^[a-zA-Z][a-zA-Z0-9_]*(\.[a-zA-Z][a-zA-Z0-9_]*)+$/);
export const ProfileNameSchema = Joi.string().pattern(/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/);

const CONTROL_CHARACTER = /[\u0000-\u001f\u007f-\u009f]/u;

function isResourceName(value: string): boolean {
  return (
    Buffer.byteLength(value, 'utf8') <= 128 &&
    value !== '.' &&
    value !== '..' &&
    !value.includes('/') &&
    !value.includes('\\') &&
    !value.includes('*') &&
    !CONTROL_CHARACTER.test(value)
  );
}

export const ResourceNameSchema = Joi.string()
  .custom((value: string, helpers) =>
    isResourceName(value) ? value : helpers.error('string.resourceName')
  )
  .messages({ 'string.resourceName': '{{#label}} is not a valid xprem resource name' });

const AndroidProfileSchema = Joi.object({
  applicationId: ApplicationIdSchema.required(),
  mode: Joi.string().valid('debug', 'release').required(),
  artifact: Joi.string().valid('apk', 'aab').required(),
  developmentClient: Joi.boolean(),
})
  .when(Joi.object({ mode: Joi.valid('debug').required() }).unknown(), {
    then: Joi.object({ artifact: Joi.valid(Joi.override, 'apk') }),
  })
  .when(Joi.object({ developmentClient: Joi.valid(true).required() }).unknown(), {
    then: Joi.object({
      mode: Joi.valid(Joi.override, 'debug'),
      artifact: Joi.valid(Joi.override, 'apk'),
    }),
  });

const BuildProfileSchema = Joi.object({
  channel: ResourceNameSchema,
  environment: ResourceNameSchema,
  android: AndroidProfileSchema.required(),
}).oxor('channel', 'environment');

export const XpremConfigSchema = Joi.object<XpremConfig>({
  schemaVersion: Joi.number().valid(1).required(),
  profiles: Joi.object().pattern(ProfileNameSchema, BuildProfileSchema).min(1).required(),
}).required();

export const CONFIG_VALIDATION_OPTIONS = {
  allowUnknown: false,
  abortEarly: false,
  convert: false,
  noDefaults: true,
};
