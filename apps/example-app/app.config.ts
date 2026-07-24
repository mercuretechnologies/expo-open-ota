import { ExpoConfig } from '@expo/config-types'
import { ConfigContext } from '@expo/config'

export default ({ config }: ConfigContext): ExpoConfig => {
  return {
    ...(config as ExpoConfig),
    runtimeVersion: '3.0.0',
    updates: {
      url: 'https://otatest.ngrok.io/manifest',
      "codeSigningMetadata": (process.env.DISABLE_CODE_SIGNING ? undefined : { keyid: 'main', alg: 'rsa-v1_5-sha256' }),
      "codeSigningCertificate": (process.env.DISABLE_CODE_SIGNING ? undefined : './certs/certificate.pem'),
      "enabled": true,

      "requestHeaders": {
        "expo-channel-name": process.env.RELEASE_CHANNEL,
        "expo-app-id": "d3a60200-4574-4781-9734-c174b511fb55",
      },
    },
  };
}
