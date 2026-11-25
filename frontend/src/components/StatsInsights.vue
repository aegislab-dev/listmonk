<template>
  <div data-cy="stats-insights">
    <h2 class="is-size-6 has-text-weight-semibold mb-1">
      ③ {{ $t('stats.next') }}
      <span class="st-note has-text-weight-normal">{{ $t('stats.nextScope', { scope: scope }) }} · {{ $t('stats.observed') }}</span>
    </h2>
    <div class="st-chips">
      <button v-for="c in cards" :key="c.key" type="button" :class="{ on: only === c.key }" @click="only = only === c.key ? '' : c.key">{{ c.title }}</button>
    </div>
    <div class="st-grid2">
      <div v-for="c in visible" :key="c.key" class="st-card st-ic" :data-cy="`card-${c.key}`">
        <h3>{{ c.title }} <stats-help :field="c.help" /></h3>
        <div class="concl" :data-cy="`concl-${c.key}`">{{ c.concl }}</div>
        <div v-if="c.insuf" class="st-insuf">{{ c.insuf }}</div>
        <div v-for="(r, i) in c.rows" :key="i" class="st-br" :class="{ mute: r.mute }">
          <span>{{ r.label }}</span>
          <span class="tr"><span class="fl" :class="{ best: r.best }" :style="{ width: r.mute ? '0%' : `${Math.min(100, (r.click / (c.scale || 12)) * 100)}%` }" /></span>
          <span class="nn">
            <template v-if="r.text">{{ r.text }}</template>
            <template v-else><b>{{ r.main }}</b> · {{ r.sub }}</template>
            <span v-if="r.best" class="st-best"> {{ $t('stats.best') }}</span>
          </span>
        </div>
        <div v-if="c.foot" class="foot">{{ c.foot }}</div>
      </div>
    </div>
  </div>
</template>

<script>
/* eslint-disable max-len, vue/max-len */
import StatsHelp from './StatsHelp.vue';
import {
  groupBy, listGroups, overlapRows, recipientRows, followupPairs, weekdayOf, hourBandOf, lengthBandOf, WEEKDAYS, HOUR_BANDS, LENGTH_BANDS, fmt, pctText, clickRate,
} from '../stats';
import { MIN } from '../stats-definitions';

const AGE_KEYS = ['new7', 'new30', 'older'];

export default {
  name: 'StatsInsights',
  components: { StatsHelp },
  props: {
    camps: { type: Array, required: true },
    // Recipient groups of the server for the team ('' = everyone), by age and by country.
    age: { type: Array, default: () => [] },
    country: { type: Array, default: () => [] },
    teamKey: { type: String, default: '' },
    scope: { type: String, default: '' },
    now: { type: Date, default: () => new Date() },
  },
  data() { return { only: '' }; },
  computed: {
    visible() { return this.only ? this.cards.filter((c) => c.key === this.only) : this.cards; },
    cards() {
      return [this.listCard(), this.ageCard(), this.bucketCard('weekday', '요일 (KST 시작 요일)', WEEKDAYS.map((w) => `${w}요일`), weekdayOf),
        this.bucketCard('hour', '시각 (KST 6시간 띠)', HOUR_BANDS, hourBandOf), this.bucketCard('length', '제목 길이', LENGTH_BANDS, lengthBandOf),
        this.countryCard(), this.overlapCard(), this.followupCard()].filter(Boolean);
    },
  },
  methods: {
    base(row) { return { main: `클릭 ${pctText(row.a ? row.a.click : row.click)}`, sub: `열람 ${pctText(row.a ? row.a.open : row.open)}` }; },
    listCard() {
      const g = listGroups(this.camps, this.now);
      const rows = g.rows.map((r) => ({
        label: r.label, click: r.a.click, best: g.best === r, mute: !r.ok, ...this.base(r), sub: `열람 ${pctText(r.a.open)} · 발송 ${fmt(r.a.sRate)}`,
      }));
      return {
        key: 'list',
        title: '리스트(출처) · 클릭률',
        help: 'click',
        rows,
        concl: g.best ? `내 팀 리스트 중 「${g.best.label}」이 클릭률 ${pctText(g.best.a.click)}로 가장 높습니다 (발송 순서 아님).` : '이름을 보여 줄 수 있는 리스트가 둘 미만이라 순위를 매기지 않습니다.',
        foot: '다른 팀 리스트는 이름 없이 합쳐서만 보입니다. 리스트 하나가 캠페인 하나인 경우가 많아 리스트와 내용을 나눌 수 없습니다.',
      };
    },
    bucketCard(key, title, labels, fn) {
      const g = groupBy(this.camps, labels, fn, this.now);
      const rows = g.rows.map((r) => (r.ok
        ? {
          label: r.label, click: r.a.click, best: g.best === r, main: `클릭 ${pctText(r.a.click)}`, sub: `열람 ${pctText(r.a.open)} · ${r.n}건 · 발송 ${fmt(r.a.sRate)}`,
        }
        : { label: r.label, mute: true, text: r.n ? `표본 작음 · ${r.n}건 (${MIN.groupCampaigns}건 필요)` : '보낸 적 없음' }));
      let concl;
      if (g.ok.length >= 2) {
        const o = [...g.ok].sort((a, b) => b.a.click - a.a.click);
        concl = `기준(묶음마다 캠페인 ${MIN.groupCampaigns}건·발송 ${fmt(MIN.groupSent)})을 채운 ${o.map((r) => r.label.replace('요일', '')).join(' · ')}만 비교합니다. ${o[0].label.replace('요일', '')}이(가) ${o[1].label.replace('요일', '')}보다 클릭률이 ${(o[0].a.click - o[1].a.click).toFixed(1)}%p 높습니다.`;
      } else {
        concl = `비교하려면 묶음마다 캠페인 ${MIN.groupCampaigns}건이 필요합니다.`;
      }
      return {
        key, title, help: 'click', rows, concl,
      };
    },
    ageCard() {
      const rows = recipientRows(this.age, this.teamKey, AGE_KEYS);
      if (!rows.length) {
        return null;
      }
      const label = { new7: '새 주소(발송 7일 안)', new30: '30일 안', older: '기존 주소' };
      const ok = rows.filter((r) => r.ok);
      const best = ok.length >= 2 ? ok.reduce((b, r) => (r.click > b.click ? r : b)) : null;
      const newer = rows.find((r) => r.key === 'new7');
      const older = rows.find((r) => r.key === 'older');
      return {
        key: 'age',
        title: '새 주소 대 기존 주소',
        help: 'click',
        rows: rows.map((r) => (r.ok
          ? {
            label: label[r.key], click: r.click, best: best === r, main: `클릭 ${pctText(r.click)}`, sub: `열람 ${pctText(r.open)} · 반송 ${pctText(r.bounce)} · 수신자 ${fmt(r.sent)}`,
          }
          : { label: label[r.key], mute: true, text: `표본 작음 · 수신자 ${fmt(r.sent)}` })),
        concl: newer && older && newer.ok && older.ok
          ? `발송 7일 안에 들어온 주소는 클릭률이 기존 주소보다 ${Math.abs(older.click - newer.click).toFixed(1)}%p ${newer.click < older.click ? '낮고' : '높고'} 반송률은 ${Math.abs(newer.bounce - older.bounce).toFixed(1)}%p ${newer.bounce > older.bounce ? '높습니다' : '낮습니다'}.`
          : '비교하려면 묶음마다 수신자가 충분해야 합니다.',
        foot: `발송 기록 ${fmt(rows.reduce((s, r) => s + r.sent, 0))}통 기준 · 캠페인 단위 숫자와 분모가 다를 수 있습니다.`,
      };
    },
    countryCard() {
      const rows = recipientRows(this.country, this.teamKey);
      const total = rows.reduce((s, r) => s + r.sent, 0);
      const named = rows.filter((r) => r.key !== '(none)');
      // The card needs the country to be filled in for most recipients.
      const none = rows.find((r) => r.key === '(none)');
      if (!rows.length || (none && none.sent > total / 2)) {
        return null;
      }
      const ok = named.filter((r) => r.ok && r.key !== '__other__');
      const best = ok.length >= 2 ? ok.reduce((b, r) => (r.click > b.click ? r : b)) : null;
      const label = (r) => ({ __other__: `기타(${MIN.recipientGroup}명 미만 묶음)`, '(none)': '미입력' }[r.key] || r.key);
      return {
        key: 'country',
        title: '국가 (수신자 country)',
        help: 'click',
        rows: rows.map((r) => ({
          label: label(r), click: r.click, best: best === r, main: `클릭 ${pctText(r.click)}`, sub: `열람 ${pctText(r.open)} · 수신자 ${fmt(r.sent)}`, mute: false,
        })),
        concl: best ? `${best.key} 수신자의 클릭률이 ${pctText(best.click)}로 가장 높습니다. 수신자 ${MIN.recipientGroup}명 미만인 국가는 「기타」로 합쳤습니다.` : `수신자 ${MIN.recipientGroup}명 미만인 국가는 「기타」로 합쳤습니다. 비교할 국가가 둘 미만입니다.`,
        foot: `발송 기록 ${fmt(total)}통 기준.`,
      };
    },
    overlapCard() {
      const o = overlapRows(this.camps, this.now);
      if (!o.rows.length) {
        return {
          key: 'overlap', title: '최근 발송 겹침', help: 'sent', rows: [], concl: '발송 기록이 발송 수의 95% 이상인 캠페인이 없어 계산하지 않았습니다.',
        };
      }
      const top = o.rows.slice(0, 5);
      const rest = o.rows.slice(5);
      const rows = top.map((r) => ({
        label: `#${r.id} ${r.name.slice(0, 8)}`, click: r.rate * 1.2, main: pctText(r.rate), sub: `${fmt(r.overlap)}명 / ${fmt(r.ledger)}명`,
      }));
      if (rest.length) {
        rows.push({ label: `그 밖 ${rest.length}건 합계`, mute: true, text: `${fmt(rest.reduce((s, r) => s + r.overlap, 0))}명` });
      }
      return {
        key: 'overlap',
        title: '최근 발송 겹침',
        help: 'sent',
        rows,
        concl: `이 기간 캠페인 수신자의 ${pctText(o.rate)}(${fmt(o.overlap)}명)가 직전 30일 안에 다른 메일을 받았습니다. 겹침이 큰 순서입니다.`,
        foot: '발송 기록이 발송 수의 95% 이상인 캠페인만 계산합니다 · 막대는 0~10% 눈금.',
      };
    },
    followupCard() {
      const pairs = followupPairs(this.camps, this.now);
      if (!pairs.length) {
        return {
          key: 'followup', title: '후속 메일 효과', help: 'click', rows: [], concl: '아직 후속 메일을 보낸 캠페인이 없습니다.', insuf: '후속 메일을 완료하면 원본과 후속의 클릭률이 여기 보입니다. 완료된 캠페인 화면에서 「후속 메일」을 만들 수 있습니다.',
        };
      }
      const rows = pairs.map((p) => ({
        label: `#${p.followup.id} ← #${p.followup.followupOf}`,
        click: clickRate(p.followup),
        main: `후속 클릭 ${pctText(clickRate(p.followup))}`,
        sub: `발송 ${fmt(p.followup.sent)}${p.original ? ` · 원본 ${pctText(clickRate(p.original))}` : ''}`,
      }));
      return {
        key: 'followup', title: '후속 메일 효과', help: 'click', rows, concl: `후속 메일 ${pairs.length}건: 원본에서 반응하지 않은 사람에게 보낸 메일의 클릭률입니다.`, foot: '후속은 원본을 누르지 않은 사람이 대상이라 원본과 단순 합산하지 않습니다.',
      };
    },
  },
};
</script>
