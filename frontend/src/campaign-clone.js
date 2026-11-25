import dayjs from 'dayjs';
import { splitAttribs, TZ_ATTRIB } from './schedule-time';

// Use fresh settings and the original body. The new draft uses only the chosen lists.
export default async function cloneCampaign(api, now, id, name, listIds) {
  const config = await api.getServerConfig();
  const c = await api.getCampaign(id);
  const list = config.from_addresses || [];
  const accountSenders = (config.sender_domains || []).length > 0;
  const sendLater = !!c.sendAt;
  let sendAt = null;
  if (sendLater) {
    sendAt = dayjs(c.sendAt).isAfter(now) ? c.sendAt : now.add(7, 'day');
  }

  // With a From list a custom From header is refused, so a copy goes without one.
  const fromHeaderFree = !accountSenders && list.length > 0;
  const dropFromHeader = (set) => Object.fromEntries(Object.entries(set).filter(([k]) => k.trim().toLowerCase() !== 'from'));

  const zone = splitAttribs(c.attribs);
  const data = {
    name,
    subject: c.subject,
    lists: listIds,
    type: c.type,
    from_email: c.fromEmail,
    reply_to: c.replyTo,
    content_type: c.contentType,
    messenger: c.messenger,
    tags: c.tags,
    template_id: c.templateId,
    body: c.body,
    body_source: c.bodySource,
    altbody: c.altbody,
    headers: fromHeaderFree ? (c.headers || []).map(dropFromHeader).filter((h) => Object.keys(h).length) : c.headers,
    send_later: sendLater,
    send_at: sendAt,
    // The copy keeps the time zone the scheduled time was chosen in.
    attribs: sendLater && zone.stored ? { [TZ_ATTRIB]: zone.tz } : undefined,
    archive: c.archive,
    archive_template_id: c.archiveTemplateId,
    archive_meta: c.archiveMeta,
    media: (c.media || []).map((m) => m.id),
  };

  if (c.archive) {
    data.archive_slug = `${name.toLowerCase().replace(/[^a-z0-9]/g, '-')}-${Date.now().toString().slice(-4)}`;
  }

  return api.createCampaign(data);
}
