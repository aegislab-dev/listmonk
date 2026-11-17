// Teams (LST-24): a team is a list tag that starts with "팀:" and the list role that holds
// the lists carrying it. Plain data helpers; the server decides who sees what.
export const TEAM_PREFIX = '팀:';
export const NO_TEAM = '__none__';

export const isTeamTag = (tag) => typeof tag === 'string' && tag.startsWith(TEAM_PREFIX) && tag.length > TEAM_PREFIX.length;

// The team tags among a list's tags, and the tags that are not team tags.
export const teamTags = (tags) => (tags || []).filter(isTeamTag);
export const plainTags = (tags) => (tags || []).filter((t) => !isTeamTag(t));

// The name shown for a team tag ("팀:마케팅2팀" -> "마케팅2팀").
export const teamName = (tag) => (isTeamTag(tag) ? tag.slice(TEAM_PREFIX.length) : tag);

// The tag a team name makes: whitespace becomes a dash, as list tags are stored.
export const teamTagOf = (name) => `${TEAM_PREFIX}${String(name || '').trim().split(/\s+/)
  .filter(Boolean)
  .join('-')}`;

// The team tags of a list that is another team's from the point of view of `mine` (the team tag
// of the person, '' for nobody's team): a list with team tags, none of them mine. A list
// without a team is nobody's other team.
export function otherTeamTags(tags, mine) {
  const t = teamTags(tags);
  return t.length > 0 && !t.includes(mine) ? t : [];
}

// The tags to send when a list is saved: its own tags without team tags, then the team choice.
// `choice` is a team tag, NO_TEAM (no team on purpose) or '' (leave the list's teams as they
// are). Returns { tags, noTeam }.
export function tagsForSave(tags, current, choice) {
  const plain = plainTags(tags);
  if (choice === NO_TEAM) return { tags: plain, noTeam: true };
  if (choice) return { tags: [...plain, choice], noTeam: false };
  return { tags: [...plain, ...teamTags(current)], noTeam: false };
}

// The team tags a new list starts with: the one team asked for (a team filter, a base list).
export function defaultTeamTag(candidates, known) {
  const knownTags = new Set((known || []).map((t) => t.tag));
  return (candidates || []).find((t) => knownTags.has(t)) || '';
}
