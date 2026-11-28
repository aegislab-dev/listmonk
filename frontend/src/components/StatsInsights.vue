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
        <div v-if="c.about" class="about" :data-cy="`about-${c.key}`">{{ c.about }}</div>
        <div v-if="c.concl" class="concl" :data-cy="`concl-${c.key}`">{{ c.concl }}</div>
        <div v-for="p in c.pairs || []" :key="p.id" class="st-fu" :class="{ mute: p.collecting }" :data-cy="`followup-${p.id}`">
          <div class="who">
            <div data-cy="followup-original">
              <span class="k">{{ $t('stats.followupOriginal') }}</span>
              <span><b>#{{ p.parent.id }}</b> {{ p.parent.name }}
                <span v-if="p.mine" class="st-pill">{{ $t('stats.myTeam') }}</span><span v-else class="st-tag">{{ p.teams }}</span>
                <span class="s">{{ p.parent.meta }}</span></span>
            </div>
            <div data-cy="followup-child">
              <span class="k">{{ $t('stats.followupFollowup') }}</span>
              <span><b>#{{ p.id }}</b> {{ p.name }} <span class="s">{{ p.meta }}</span></span>
            </div>
          </div>
          <div class="what" data-cy="followup-result">
            <div>
              <span class="k">{{ $t('stats.click') }}</span>
              <span v-if="p.text">{{ p.text }}</span>
              <span v-else>{{ p.lead }} <b>{{ p.after }}</b> <span v-if="p.gain" class="st-chg good">{{ p.gain }}</span></span>
            </div>
            <span v-if="!p.text" class="tr"><span class="fl" :style="{ width: p.bar[0] }" /><span class="fl best" :style="{ left: p.bar[0], width: p.bar[1] }" /></span>
            <div class="s">{{ p.sub }}</div>
          </div>
        </div>
        <div v-for="(r, i) in c.rows" :key="i" class="st-br" :class="{ mute: r.mute }">
          <span>{{ r.label }}</span>
          <span class="tr"><span class="fl" :class="{ best: r.best }" :style="{ width: r.click == null ? '0%' : `${Math.min(100, (r.click / (c.scale || 12)) * 100)}%` }" /></span>
          <span class="nn">
            <template v-if="r.text">{{ r.text }}</template>
            <template v-else><b>{{ r.main }}</b> · {{ r.sub }}</template>
            <span v-if="r.best" class="st-best"> {{ $t('stats.best') }}</span>
          </span>
        </div>
        <div v-if="c.small" class="left" :data-cy="`small-${c.key}`">{{ c.small }}</div>
        <div v-if="c.merged" class="left" :data-cy="`merged-${c.key}`">{{ c.merged }}</div>
        <div v-if="c.foot" class="foot">{{ c.foot }}</div>
        <router-link v-if="c.followupSource" class="button is-small is-outlined mt-2" data-cy="insight-followup-start"
          :to="{ name: 'campaign', params: { id: c.followupSource } }">
          {{ $t('stats.followupStart') }}
        </router-link>
      </div>
    </div>
  </div>
</template>

<script>
/* eslint-disable max-len, vue/max-len */
import StatsHelp from './StatsHelp.vue';
import {
  groupBy, listGroups, overlapRows, recipientRows, followupPairs, weekdayOf, lengthBandOf, fmt, pct, pctText, clickRate, classify, signedPoints,
  campaignTeams, teamLabel, formatKST,
} from '../stats';
import { conditionWords } from '../followup';
import { MIN } from '../stats-definitions';

const AGE_KEYS = ['new7', 'new30', 'older'];

export default {
  name: 'StatsInsights',
  components: { StatsHelp },
  props: {
    camps: { type: Array, required: true },
    // Every team's campaigns of the period, to name the original of a follow-up (the team filter may leave it out).
    all: { type: Array, default: null },
    myTeam: { type: String, default: '' },
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
        this.bucketCard('weekday', weekdayOf), this.bucketCard('length', lengthBandOf),
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
    // The line under a list for the groups left out of it: how many, and the sample they did not reach.
    leftOut(n, rule, params) {
      return n ? this.t('smallLeftOut', { n, rule: this.t(rule, params) }) : '';
    },
    // Bars are drawn against 12%, or against the highest rate in the list when that is higher.
    scaleOf(rows) {
      return Math.max(12, ...rows.map((r) => r.click || 0));
    },
    highest(label, rate) {
      return this.t('bestRate', { label, rate: pctText(rate) });
    },
    compareClicks(label, rate, baseline, baselineRate) {
      return this.t('clickRateCompare', { label, baseline, delta: signedPoints(rate - baselineRate) });
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
      const rows = g.listed.map((r) => {
        const { main, sub } = this.rates(r.a);
        sub.push(this.t('sentCount', { n: fmt(r.a.sent) }));
        if (r.list?.createdAt && !r.list.source) {
          const date = new Intl.DateTimeFormat(this.$i18n.locale, { timeZone: 'Asia/Seoul', dateStyle: 'medium' });
          sub.push(this.t('listDate', { date: date.format(new Date(r.list.createdAt)) }));
        }
        return {
          label: this.listLabel(r), main, sub: sub.join(' · '), click: r.a.click, best: g.best === r,
        };
      });
      return {
        key: 'list',
        title: this.t('listTitle'),
        help: 'click',
        rows,
        scale: this.scaleOf(rows),
        small: this.leftOut(g.small, 'smallRuleList', { n: MIN.campaignSent }),
        concl: g.best ? this.highest(this.listLabel(g.best), g.best.a.click) : this.t('listInsufficient', { n: MIN.campaignSent }),
        foot: this.t('listFoot'),
      };
    },
    bucketCard(key, fn) {
      const g = groupBy(this.camps, this.t(`${key}Labels`).split('|'), fn, this.now);
      const next = g.ok.filter((r) => r !== g.best).sort((a, b) => b.a.click - a.a.click)[0];
      const rows = g.listed.map((r) => {
        if (!r.n) return { label: r.label, mute: true, text: this.t('noSend') };
        const { main, sub } = this.rates(r.a);
        sub.push(this.t('groupCount', { n: r.n }), this.t('sentCount', { n: fmt(r.a.sent) }));
        return {
          label: r.label, click: r.a.click, best: g.best === r, main, sub: sub.join(' · '),
        };
      });
      return {
        key,
        title: this.t(`${key}Title`),
        help: 'click',
        rows,
        scale: this.scaleOf(rows),
        small: this.leftOut(g.small, 'smallRuleGroup', { campaigns: MIN.groupCampaigns, sent: fmt(MIN.groupSent) }),
        concl: g.best
          ? this.compareClicks(g.best.label, g.best.a.click, next.label, next.a.click)
          : this.t('groupInsufficient', { campaigns: MIN.groupCampaigns, sent: fmt(MIN.groupSent) }),
      };
    },
    recipientCard(key) {
      const country = key === 'country';
      const data = recipientRows(country ? this.country : this.age, this.teamKey, country ? null : AGE_KEYS);
      const total = data.reduce((s, r) => s + r.sent, 0);
      const missing = data.find((r) => r.key === '(none)');
      const hasNamedCountry = data.some((r) => r.key !== '(none)' && r.key !== '__other__');
      if (!data.length || (country && (!hasNamedCountry || (missing && missing.sent > total / 2)))) return null;
      const eligible = data.filter((r) => r.click !== null && r !== missing);
      eligible.sort((a, b) => b.click - a.click);
      const [leader, next] = eligible;
      const best = next ? leader : null;
      const label = (r) => {
        const labelKey = {
          new7: 'ageNew', new30: 'ageMonth', older: 'ageOld', '(none)': 'countryMissing',
        }[r.key];
        return labelKey ? this.t(labelKey) : r.key;
      };
      // Not listed: groups with too small a sample (counted under the list), and the group the server
      // merged from classes below MIN.recipientGroup people (named under the list, whatever its size).
      const listed = data.filter((r) => !r.small && !r.merged);
      const merged = data.find((r) => r.merged);
      const rows = listed.map((r) => {
        const count = this.t('recipientOverallCount', { n: fmt(r.recipients) });
        const trackedSample = this.t('recipientTrackedSample', { n: fmt(r.trackedRecipients), sent: fmt(r.trackedSent) });
        if (r.click === null) {
          return { label: label(r), main: this.t('untracked'), sub: `${this.t('bounceValue', { rate: pctText(r.bounce) })} · ${count}` };
        }
        const { main, sub } = this.rates(r);
        sub.push(trackedSample);
        if (r.trackedSent !== r.sent) sub.push(count);
        return {
          label: label(r), click: r.click, best: best === r, main, sub: sub.join(' · '),
        };
      });
      let concl = this.t('recipientInsufficient', { n: MIN.recipientSample });
      if (best && country) concl = this.highest(label(best), best.click);
      if (best && !country) {
        const fresh = eligible.find((r) => r.key === 'new7');
        const older = eligible.find((r) => r.key === 'older');
        concl = fresh && older ? this.t('ageRateCompare', {
          baseline: label(older),
          click: signedPoints(fresh.click - older.click),
          bounce: signedPoints(fresh.bounce - older.bounce),
        }) : this.compareClicks(label(best), best.click, label(next), next.click);
      }
      return {
        key,
        title: this.t(`${key}Title`),
        help: 'click',
        rows,
        scale: this.scaleOf(rows),
        small: this.leftOut(data.filter((r) => r.small).length, 'smallRuleRecipient', { n: MIN.recipientSample }),
        merged: merged ? this.t('mergedLeftOut', { n: MIN.recipientGroup, people: fmt(merged.recipients) }) : '',
        concl,
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
    // One follow-up and its original: which campaigns, who got the second mail, and what it added to the
    // original's click rate. Every value is on the row once.
    followupCard() {
      const pairs = followupPairs(this.camps, this.all || this.camps);
      const scale = Math.max(12, ...pairs.map((p) => p.click || 0));
      const meta = (c) => [formatKST(c.startedAt, this.$i18n.locale), this.t('sentCount', { n: fmt(c.sent) })];
      const rows = pairs.map((p) => {
        const c = p.followup;
        const { untracked, collecting, hours } = classify(c, this.now);
        const condition = c.followupCondition ? conditionWords(c.followupCondition, this.$t.bind(this)) : '';
        const everyone = condition === this.$t('campaigns.reactionList.received');
        const audience = condition && this.t(everyone ? 'followupAudienceAll' : 'followupAudience', { condition });
        const teams = campaignTeams(c);
        const row = {
          id: c.id,
          name: c.name,
          meta: [...meta(c), audience].filter(Boolean).join(' · '),
          parent: {
            id: c.followupOf,
            name: p.parent ? p.parent.name : '',
            meta: p.parent ? meta(p.parent).join(' · ')
              : [this.t('followupOutside'), this.t('sentCount', { n: fmt(c.followupParentSent) })].join(' · '),
          },
          mine: !!this.myTeam && teams.includes(this.myTeam),
          teams: teams.map((tag) => teamLabel(tag, this.$t.bind(this))).join(', '),
          collecting,
        };
        if (untracked || p.click === null) return { ...row, text: this.t('untracked'), sub: collecting ? this.t('collectingLong', { h: hours }) : '' };
        const known = p.parentClick !== null;
        const sub = [];
        if (collecting) sub.push(this.t('collectingLong', { h: hours }));
        sub.push(known
          ? this.t('followupClickers', { before: fmt(p.parent.clickers), after: fmt(c.combinedClickers) })
          : this.t('followupClickersAfter', { after: fmt(c.combinedClickers) }));
        sub.push(known
          ? this.t('followupOpens', { before: pctText(p.parentOpen), after: pctText(p.open) })
          : this.t('followupOpensAfter', { after: pctText(p.open) }));
        sub.push(this.t('followupOwnRate', { rate: pctText(clickRate(c)) }));
        if (!known) sub.push(this.t(p.parent ? 'followupOriginalUntracked' : 'followupOutsideNote'));
        const before = known ? p.parentClick : 0;
        return {
          ...row,
          lead: known ? this.t('followupLift', { before: pctText(p.parentClick) }) : this.t('followupLiftAfter'),
          after: pctText(p.click),
          // The difference of the two rates as they are shown, so that 2.1% → 3.1% reads +1.0%p.
          gain: known ? signedPoints(Number(p.click.toFixed(1)) - Number(p.parentClick.toFixed(1))) : '',
          // Grey: the original alone. Blue: what the follow-up added (the whole bar when the original is not known).
          bar: [`${Math.min(100, (before / scale) * 100)}%`, `${Math.min(100, (Math.max(0, p.click - before) / scale) * 100)}%`],
          sub: sub.join(' · '),
        };
      });
      const canCreate = this.$canCreateList() && this.$can('campaigns:get', 'campaigns:get_all')
        && this.$can('campaigns:manage', 'campaigns:manage_all');
      const source = !rows.length && canCreate && this.camps.find((c) => c.own && c.status === 'finished' && !c.followupOf);
      return {
        key: 'followup',
        title: this.t('followupTitle'),
        help: 'followup',
        about: this.t('followupAbout'),
        rows: [],
        pairs: rows,
        concl: rows.length ? '' : this.t('followupEmpty'),
        foot: rows.length ? this.t('followupExplain') : '',
        followupSource: source ? source.id : null,
      };
    },
  },
};
</script>
