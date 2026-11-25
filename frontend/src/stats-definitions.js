// Statistics dashboard definitions (LST-64): the numbers every statistics screen shares
// (thresholds, baselines) and the words that explain them. The "?" next to a figure and the
// folded "metric definitions" table read the same entries, so a sentence lives in one place.
// Plain data, no browser or Vue. The text is Korean whatever language the screen has, as in
// proof-summary.js.

/* eslint-disable max-len, vue/max-len */
// Sample thresholds. A campaign below `campaignSent` is shown grey as "표본 작음"; a team or a
// group needs `groupCampaigns` finished campaigns and `groupSent` messages.
export const MIN = {
  campaignSent: 200,
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
export const PERIOD_ALL_DAYS = 790;

// One entry per figure: name, one-line meaning (easy words), what it is compared with, the
// formula and the minimum sample (shown when a row is opened, and in the "?").
export const DEFINITIONS = [
  {
    key: 'sent', name: '발송', meaning: '메일 서버에 넘긴 메일 수 (수신함에 도착한 수는 아님)', compare: '대상 수', formula: 'campaigns.sent · 완료 캠페인만, 진행 중은 제외', sample: '—',
  },
  {
    key: 'click', name: '클릭률', meaning: '메일을 받은 사람 중 링크를 한 번이라도 누른 사람의 비율', compare: '직전 90일 · 소속 팀 중앙값 · 전사', formula: '고유 클릭자 ÷ 발송 · 집계가 끝나고 추적이 켜진 캠페인만', sample: '캠페인 발송 200 · 팀/묶음 캠페인 3건 그리고 1,000통',
  },
  {
    key: 'open', name: '열람률 (참고)', meaning: '메일을 받은 사람 중 메일을 연 것으로 잡힌 사람의 비율 — 실제와 다를 수 있어 참고용', compare: '직전 90일', formula: '고유 열람자 ÷ 발송 · 이미지 차단·자동 불러오기로 오차', sample: '클릭률과 같음',
  },
  {
    key: 'bounce', name: '반송률', meaning: '받는 쪽 서버가 돌려보낸 메일의 비율 — 2%를 넘으면 빨간 ⚠', compare: '기준선 2% · 직전 90일', formula: '(hard+soft 반송) ÷ 발송 · 증빙 요약의 반송 = 반송 + 신고', sample: '클릭률과 같음',
  },
  {
    key: 'complaint', name: '신고율', meaning: '받은 사람이 스팸으로 신고한 비율 — 0.1%를 넘으면 빨간 ⚠', compare: '기준선 0.1%', formula: 'complaint ÷ 발송', sample: '발송 1,000',
  },
  {
    key: 'neutral', name: '비슷함', meaning: '변화가 아주 작아 차이로 보지 않는다는 뜻', compare: '클릭 0.5 · 열람 1.0 · 반송 0.3 %p 안', formula: '한두 건에 뒤집히지 않는 폭', sample: '—',
  },
  {
    key: 'small', name: '표본 작음', meaning: '메일 수가 적어 비교하기 이르다는 뜻 — 회색으로 보이고 비교에서 빠짐', compare: '—', formula: '문턱 미만이면 중앙값·「가장 높음」에서 제외', sample: '캠페인 발송 200 · 팀/묶음 캠페인 3건 그리고 1,000통',
  },
  {
    key: 'collecting', name: '집계 중', meaning: '보낸 지 3일이 안 돼 열람·클릭이 아직 쌓이는 중', compare: '—', formula: '회색, 팀 합계·중앙값·직전 대비에서 빠짐(발송 수에는 포함)', sample: '—',
  },
  {
    key: 'untracked', name: '추적 없음', meaning: '추적이 꺼져 열람·클릭이 기록되지 않은 캠페인 — 0%가 아님', compare: '—', formula: '추적이 켜져 있는데 0이면 0%로 보임', sample: '—',
  },
  {
    key: 'median', name: '소속 팀 중앙값', meaning: '그 캠페인을 보낸 팀 캠페인들의 클릭률 가운데 값', compare: '—', formula: '다른 팀 캠페인은 그 팀 이름을 적음 · 진행 중·집계 중·추적 없음은 뺌', sample: '팀 비율 계산 3건',
  },
];

export const definitionOf = (key) => DEFINITIONS.find((d) => d.key === key);

// The text of a "?" for a figure.
export function helpText(key) {
  const d = definitionOf(key);
  if (!d) {
    return '';
  }
  return `${d.meaning}\n계산식: ${d.formula}\n비교: ${d.compare}\n최소 표본: ${d.sample}`;
}
