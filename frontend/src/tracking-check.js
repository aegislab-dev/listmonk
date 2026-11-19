// The pre-send tracking check (LST-44): what the box, the start confirmation and the
// test send notice say for a report from POST /api/campaigns/:id/tracking-check.
// Everything here returns i18n keys with their params; the components translate.

const K = 'campaigns.tracking';

// The tag at the head of the box.
export const trackingTag = (report) => {
  switch (report?.state) {
    case 'ok': return { key: `${K}.ok`, type: 'is-success is-light' };
    case 'warn': return { key: `${K}.warn`, type: 'is-warning' };
    case 'off': return { key: `${K}.off`, type: 'is-danger is-light' };
    default: return { key: `${K}.failed`, type: 'is-light' };
  }
};

const viewRow = (r) => {
  const row = (level, help, helpParams) => ({
    name: 'view', level, label: `${K}.view`, state: `${K}.view.${r.view === 'n/a' ? 'na' : r.view}`, help, helpParams,
  });
  switch (r.view) {
    case 'manual': return row('ok');
    case 'auto': return row('ok', `${K}.view.autoHelp`);
    case 'missing': return row('warn', `${K}.view.missingHelp`, { tag: '{{ TrackView }}' });
    case 'unsupported': return row('na', `${K}.view.unsupportedHelp`);
    case 'off': return row('off');
    default: return row('na', `${K}.view.naHelp`, { messenger: r.messenger });
  }
};

const clickRow = (r) => {
  const { total, auto, untracked } = r.links;
  const row = (level, state, help) => ({
    name: 'click',
    level,
    label: `${K}.click`,
    state: `${K}.click.${state}`,
    stateParams: { total, auto, untracked },
    help,
    helpParams: { tag: '@TrackLink' },
  });
  if (r.state === 'off') return row('off', 'off');
  if (total === 0) return row('na', 'none');
  if (untracked === 0) return row('ok', auto > 0 ? 'allAuto' : 'all');
  // A visual body on a messenger that is not e-mail: @TrackLink is not the first thing to say.
  const help = r.contentType === 'visual' && !r.applicable ? `${K}.click.notEmailHelp` : `${K}.click.untrackedHelp`;
  return row('warn', untracked === total ? 'noneTracked' : 'someUntracked', help);
};

// The rows of the box: views, clicks and, when it is off, the note on per-person records.
export const trackingRows = (report) => {
  if (!report) return [];
  const rows = [viewRow(report), clickRow(report)];
  if (report.state !== 'off' && !report.individual) {
    rows.push({
      name: 'individual', level: 'na', label: `${K}.individual`, state: `${K}.individual.off`, help: `${K}.individual.offHelp`,
    });
  }
  return rows;
};

// The lines of the warning box in the start/schedule confirmation. No lines: no warning.
// A report of null means that the check could not be made.
export const trackingConfirmLines = (report) => {
  if (!report) return [{ key: `${K}.confirm.unknown` }];
  if (report.state === 'off') return [{ key: `${K}.confirm.off` }];
  const lines = [];
  if (report.view === 'missing') lines.push({ key: `${K}.confirm.viewMissing` });
  if (report.links.untracked > 0) lines.push({ key: `${K}.confirm.untracked`, params: { n: report.links.untracked } });
  return lines;
};

// What the test send notice adds. Empty: the usual notice.
export const trackingToastParts = (report) => {
  if (!report) return [];
  if (report.state === 'off') return [{ key: `${K}.toast.off` }];
  const parts = [];
  if (report.view === 'missing') parts.push({ key: `${K}.toast.viewMissing` });
  if (report.links.untracked > 0) parts.push({ key: `${K}.toast.untracked`, params: { n: report.links.untracked } });
  return parts;
};

// Splits a help text on `code` marks: odd parts are code.
export const codeParts = (text) => text.split('`');
