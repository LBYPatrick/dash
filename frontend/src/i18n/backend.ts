import { t } from './index';

// Older backends expose English diagnostics, not translated interface strings.
// Keep their technical details intact; map known actionable conditions here.
export function backendMessage(message: string): string {
  if (
    /lacks managed local streaming|local streaming requires installed/.test(
      message,
    )
  )
    return t('backend.unsupportedLive');
  if (
    /requires? confirmed parked|vehicle is not parked|not confirmed parked/.test(
      message,
    )
  )
    return t('backend.parked');
  if (/2 GiB free|insufficient space above the recording reserve/.test(message))
    return t('backend.disk');
  if (
    /already active|already has a live|operation is running|processing job/.test(
      message,
    )
  )
    return t('backend.busy');
  if (/firmware streaming service|firmware rejected the stream/.test(message))
    return t('backend.liveFailed');
  if (/context canceled|context cancelled/.test(message))
    return t('backend.cancelled');
  if (/release feed HTTP/.test(message)) return t('updates.feedMissing');
  return message;
}
