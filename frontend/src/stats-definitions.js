// Statistics dashboard definitions (LST-64): the numbers every statistics screen shares
// (thresholds, baselines) and the words that explain them. The "?" next to a figure and the
// folded "metric definitions" table read the same entries, so a sentence lives in one place.
// Text is resolved by the caller using the active language.

/* eslint-disable max-len, vue/max-len */
// Sample thresholds. A campaign below `campaignSent` is shown grey as "표본 작음"; a team or a
// group needs `groupCampaigns` finished campaigns and `groupSent` messages.
export const MIN = {
  campaignSent: 200,
  complaintSent: 1000,
  recipientSample: 200,
  groupCampaigns: 3,
  groupSent: 1000,
  // People in a group of recipients; a smaller one is merged by the server (models.StatsMinGroup).
  recipientGroup: 30,
};

// A campaign younger than this is "집계 중": opens and clicks are still coming in.
export const SETTLE_DAYS = 3;

// Lines the rates are read against (percent): a bounce rate above 2% endangers the sender
// reputation (SES reviews at 5%), a complaint rate above 0.1% is too much.
export const BASELINE = { bounce: 2, complaint: 0.1 };

// A change smaller than this many percentage points is "비슷함" (not told apart).
export const NEUTRAL = {
  click: 0.5, open: 1.0, bounce: 0.3, complaint: 0.05,
};

// Default period in days and the choices (null is everything the server reads).
export const PERIOD_DEFAULT = 90;
export const PERIODS = [30, 90, 365, null];

const keys = ['sent', 'click', 'open', 'bounce', 'complaint', 'neutral', 'small', 'collecting', 'untracked', 'median'];
const fields = ['name', 'meaning', 'compare', 'formula', 'sample'];
export const DEFINITIONS = keys.map((key) => ({
  key,
  ...Object.fromEntries(fields.map((field) => [field, `stats.definition.${key}.${field}`])),
}));

// What the "?" bubble shows for a figure: its name and meaning, then one labelled row each for the
// formula, what it is compared with and the smallest sample.
export function helpParts(key, t) {
  const d = DEFINITIONS.find((entry) => entry.key === key);
  if (!d) {
    return null;
  }
  return {
    name: t(d.name),
    meaning: t(d.meaning),
    rows: [
      { key: 'formula', label: t('stats.formula'), text: t(d.formula) },
      { key: 'compare', label: t('stats.defCompare'), text: t(d.compare) },
      { key: 'sample', label: t('stats.minSample'), text: t(d.sample) },
    ],
  };
}
