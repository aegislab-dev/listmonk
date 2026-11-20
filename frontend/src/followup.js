// What a follow-up was made with, in words (LST-47). A condition with both axes on "any" is the
// whole audience of the original campaign, which the dialog calls "received".
export function conditionWords(condition, t) {
  if (!condition) return '';
  const words = ['open', 'click']
    .filter((axis) => condition[axis] && condition[axis] !== 'any')
    .map((axis) => t(`campaigns.reactionList.${axis}.${condition[axis]}`));
  return words.length ? words.join(' · ') : t('campaigns.reactionList.received');
}

export default { conditionWords };
