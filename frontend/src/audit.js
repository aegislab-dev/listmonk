// Helpers of the audit log screen (LST-27): the areas and actions it filters by, and the
// readable form of the detail an audit row carries.

export const AUDIT_AREAS = ['list', 'subscriber', 'import', 'campaign', 'access', 'settings'];

// The actions of each area, in the order the filter shows them.
export const AUDIT_ACTIONS = {
  list: ['list.create', 'list.update', 'list.team_change', 'list.delete', 'list.restore', 'list.purge',
    'preset.create', 'preset.update', 'preset.delete'],
  subscriber: ['subscriber.delete', 'subscriber.blocklist', 'subscriber.lists_add', 'subscriber.lists_remove',
    'subscriber.lists_unsubscribe', 'subscriber.gc'],
  import: ['import.start', 'import.stop', 'import.finish'],
  campaign: ['campaign.start', 'campaign.schedule', 'campaign.unschedule', 'campaign.pause', 'campaign.cancel',
    'campaign.delete', 'campaign.test'],
  access: ['role.create', 'role.update', 'role.delete', 'team.create', 'team.update', 'team.delete', 'team.members',
    'user.create', 'user.update', 'user.delete', 'user.twofa'],
  settings: ['settings.update'],
};

// Bulk and status words that are stored as English identifiers get a translated label when
// one exists (audit.value.<word>), and are shown as they are otherwise.
const isObj = (v) => v !== null && typeof v === 'object' && !Array.isArray(v);

export function auditValue(v, t, te) {
  if (v === null || v === undefined) return '';
  if (typeof v === 'boolean') return t(v ? 'audit.value.yes' : 'audit.value.no');
  if (Array.isArray(v)) return v.map((x) => auditValue(x, t, te)).filter((x) => x !== '').join(', ');
  if (isObj(v)) {
    if ('name' in v && 'id' in v) return v.name || `#${v.id}`;
    if ('from' in v && 'to' in v) return `${auditValue(v.from, t, te) || '–'} → ${auditValue(v.to, t, te) || '–'}`;
    return Object.keys(v).map((k) => `${k}: ${auditValue(v[k], t, te)}`).join(', ');
  }
  const key = `audit.value.${v}`;
  return te(key) ? t(key) : String(v);
}

// detailLines turns the detail of an audit row into [{ label, text }] lines.
export function detailLines(detail, t, te) {
  const out = [];
  Object.keys(detail || {}).forEach((k) => {
    const v = detail[k];
    if (v === null || v === undefined || (Array.isArray(v) && v.length === 0)) return;
    const label = te(`audit.detail.${k}`) ? t(`audit.detail.${k}`) : k;
    if (k === 'changed' && isObj(v)) {
      Object.keys(v).forEach((f) => {
        out.push({ label: te(`audit.detail.${f}`) ? t(`audit.detail.${f}`) : f, text: auditValue(v[f], t, te) });
      });
      return;
    }
    out.push({ label, text: auditValue(v, t, te) });
  });
  return out;
}
