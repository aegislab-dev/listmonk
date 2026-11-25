<template>
  <div data-cy="stats-insights">
    <h2 class="is-size-6 has-text-weight-semibold mb-1">
      ③ {{ $t('stats.next') }}
      <span class="st-note has-text-weight-normal">{{ $t('stats.nextScope', { scope: scope }) }} · {{ $t('stats.observed') }}</span>
    </h2>
    <div class="st-chips">
      <button type="button" :class="{ on: !only }" :aria-pressed="String(!only)" data-cy="insight-all" @click="only = ''">
        {{ $t('stats.allCards') }}
      </button>
      <button v-for="c in cards" :key="c.key" type="button" :class="{ on: only === c.key }" :aria-pressed="String(only === c.key)" @click="only = c.key">{{ c.title }}</button>
    </div>
    <div class="st-grid2">
      <div v-for="c in visible" :key="c.key" class="st-card st-ic" :data-cy="`card-${c.key}`">
        <h3>{{ c.title }} <stats-help :field="c.help" /></h3>
        <div v-if="c.concl" class="concl" :data-cy="`concl-${c.key}`">{{ c.concl }}</div>
        <div v-for="(r, i) in c.rows" :key="i" class="st-br" :class="{ mute: r.mute }">
          <span>{{ r.label }}</span>
          <span class="tr"><span class="fl" :class="{ best: r.best }" :style="{ width: r.click == null ? '0%' : `${Math.min(100, (r.click / (c.scale || 12)) * 100)}%` }" /></span>
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
  groupBy, listGroups, overlapRows, recipientRows, followupPairs, weekdayOf, hourBandOf, lengthBandOf, fmt, pct, pctText, clickRate, classify,
} from '../stats';
import { conditionWords } from '../followup';
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
    overlap: { type: Array, default: null },
    overlapError: { type: Boolean, default: false },
    teamKey: { type: String, default: '' },
    scope: { type: String, default: '' },
    now: { type: Date, default: () => new Date() },
  },
  data() { return { only: '' }; },
  computed: {
    visible() { const selected = this.cards.find((c) => c.key === this.only); return selected ? [selected] : this.cards; },
    cards() {
      return [
        this.listCard(), this.recipientCard('age'),
        this.bucketCard('weekday', weekdayOf), this.bucketCard('hour', hourBandOf), this.bucketCard('length', lengthBandOf),
        this.recipientCard('country'), this.overlapCard(), this.followupCard(),
      ].filter(Boolean);
    },
  },
  watch: {
    cards(cards) {
      if (this.only && !cards.some((card) => card.key === this.only)) this.only = '';
    },
  },
  methods: {
    t(key, params) { return this.$t(`stats.${key}`, params); },
    highest(label, rate) {
      return this.t('bestRate', { label, rate: pctText(rate) });
    },
    rates(a) {
      return {
        main: this.t('clickValue', { rate: pctText(a.click) }),
        sub: [this.t('openValue', { rate: pctText(a.open) }), this.t('bounceValue', { rate: pctText(a.bounce) })],
      };
    },
    listLabel(r) {
      if (r.hidden) return this.t('listHidden', { n: r.a.n });
      if (r.list?.source) return this.t('sourceName', { name: r.label });
      return r.label || this.t('listMultiple');
    },
    listCard() {
      const g = listGroups(this.camps, this.now);
      const rows = g.rows.map((r) => {
        const { main, sub } = this.rates(r.a);
        sub.push(this.t('sentCount', { n: fmt(r.a.sent) }));
        if (!r.ok) sub.push(this.t('small'));
        if (r.list?.createdAt && !r.list.source) {
          const date = new Intl.DateTimeFormat(this.$i18n.locale, { timeZone: 'Asia/Seoul', dateStyle: 'medium' });
          sub.push(this.t('listDate', { date: date.format(new Date(r.list.createdAt)) }));
        }
        return {
          label: this.listLabel(r), main, sub: sub.join(' · '), click: r.a.click, best: g.best === r, mute: !r.ok,
        };
      });
      return {
        key: 'list',
        title: this.t('listTitle'),
        help: 'click',
        rows,
        concl: g.best ? this.highest(this.listLabel(g.best), g.best.a.click) : this.t('listInsufficient', { n: MIN.campaignSent }),
        foot: this.t('listFoot'),
      };
    },
    bucketCard(key, fn) {
      const g = groupBy(this.camps, this.t(`${key}Labels`).split('|'), fn, this.now);
      const rows = g.rows.map((r) => {
        if (!r.n) return { label: r.label, mute: true, text: this.t('noSend') };
        const { main, sub } = this.rates(r.a);
        sub.push(this.t('groupCount', { n: r.n }), this.t('sentCount', { n: fmt(r.a.sent) }));
        if (!r.ok) sub.push(this.t('small'));
        return {
          label: r.label, click: r.a.click, best: g.best === r, mute: !r.ok, main, sub: sub.join(' · '),
        };
      });
      return {
        key,
        title: this.t(`${key}Title`),
        help: 'click',
        rows,
        concl: g.best ? this.highest(g.best.label, g.best.a.click) : this.t('groupInsufficient', { campaigns: MIN.groupCampaigns, sent: fmt(MIN.groupSent) }),
      };
    },
    recipientCard(key) {
      const country = key === 'country';
      const data = recipientRows(country ? this.country : this.age, this.teamKey, country ? null : AGE_KEYS);
      const total = data.reduce((s, r) => s + r.sent, 0);
      const missing = data.find((r) => r.key === '(none)');
      const hasNamedCountry = data.some((r) => r.key !== '(none)' && r.key !== '__other__');
      if (!data.length || (country && (!hasNamedCountry || (missing && missing.sent > total / 2)))) return null;
      const eligible = data.filter((r) => r.ok && r.trackedRecipients >= MIN.recipientSample && r.key !== '(none)');
      const best = eligible.length >= 2 ? eligible.reduce((b, r) => (r.click > b.click ? r : b)) : null;
      const label = (r) => {
        const labelKey = {
          new7: 'ageNew', new30: 'ageMonth', older: 'ageOld', '(none)': 'countryMissing', __other__: 'recipientOther',
        }[r.key];
        return labelKey ? this.t(labelKey, { n: MIN.recipientGroup }) : r.key;
      };
      const rows = data.map((r) => {
        if (!r.ok || r === missing) {
          return { label: label(r), mute: true, text: `${this.t('small')} · ${this.t('recipientCount', { n: fmt(r.recipients) })}` };
        }
        const count = this.t('recipientCount', { n: fmt(r.recipients) });
        if (r.click === null) {
          return {
            label: label(r), main: this.t('untracked'), sub: `${this.t('bounceValue', { rate: pctText(r.bounce) })} · ${count}`,
          };
        }
        const { main, sub } = this.rates(r);
        sub.push(count);
        return {
          label: label(r), click: r.click, best: best === r, main, sub: sub.join(' · '),
        };
      });
      return {
        key,
        title: this.t(`${key}Title`),
        help: 'click',
        rows,
        concl: best ? this.highest(label(best), best.click) : this.t('recipientInsufficient', { n: MIN.recipientSample }),
        foot: this.t('recipientFoot', { n: fmt(total) }),
      };
    },
    overlapCard() {
      const card = { key: 'overlap', title: this.t('overlapTitle'), help: 'sent' };
      if (!this.overlap) {
        return { ...card, rows: [], concl: this.t(this.overlapError ? 'overlapError' : 'overlapLoading') };
      }
      const {
        rows: overlaps, rate, total, recentRate,
      } = overlapRows(this.camps, this.overlap, this.teamKey);
      const rows = overlaps.slice(0, 5).map((r) => ({
        label: `#${r.id} ${r.name.slice(0, 8)}`,
        click: r.rate * 1.2,
        main: pctText(r.rate),
        sub: [
          this.t('overlapCount', { overlap: fmt(r.overlap30), ledger: fmt(r.ledger) }),
          this.t('overlapRecent', { rate: pctText(pct(r.overlap7, r.ledger)) }),
        ].join(' · '),
      }));
      const rest = overlaps.slice(5);
      if (rest.length) {
        rows.push({
          label: this.t('overlapRest', { n: rest.length }),
          mute: true,
          text: this.t('overlapRestCount', {
            overlap: fmt(rest.reduce((s, r) => s + r.overlap30, 0)),
            ledger: fmt(rest.reduce((s, r) => s + r.ledger, 0)),
          }),
        });
      }
      return {
        ...card,
        rows,
        concl: total
          ? `${this.t('overlapConcl', { rate: pctText(rate), n: fmt(total.overlap30), total: fmt(total.recipients) })} ${this.t('overlapRecent', { rate: pctText(recentRate) })}`
          : this.t('overlapEmpty'),
        foot: rows.length ? this.t('overlapFoot') : '',
      };
    },
    followupCard() {
      const rows = followupPairs(this.camps, this.now).map(({ followup: c, click, open }) => {
        const tracked = !classify(c, this.now).untracked;
        const condition = conditionWords(c.followupCondition, this.$t.bind(this));
        const sub = [this.t('sentCount', { n: fmt(c.sent) })];
        if (tracked) sub.push(this.t('followupChildCounts', { clickers: fmt(c.clickers), viewers: fmt(c.viewers) }));
        sub.push(this.t('followupCombined', { click: pctText(click), open: pctText(open) }));
        if (click !== null) sub.push(this.t('followupCombinedCounts', { clickers: fmt(c.combinedClickers), sent: fmt(c.followupParentSent) }));
        if (condition) sub.push(this.t('followupTarget', { condition }));
        return {
          label: `#${c.id} ← #${c.followupOf}`,
          click: tracked ? clickRate(c) : 0,
          main: this.t('followupChild', { click: pctText(tracked ? clickRate(c) : null), open: pctText(tracked ? pct(c.viewers, c.sent) : null) }),
          sub: sub.join(' · '),
        };
      });
      return {
        key: 'followup',
        title: this.t('followupTitle'),
        help: 'click',
        rows,
        concl: rows.length ? '' : this.t('followupEmpty'),
        foot: this.t('followupExplain'),
      };
    },
  },
};
</script>
