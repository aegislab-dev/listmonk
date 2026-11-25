<template>
  <!-- eslint-disable vue/max-len -->
  <div class="st-card" data-cy="stats-teams">
    <h2>
      ① {{ $t('stats.teams') }}
      <small>{{ $t('stats.teamsNote') }}</small>
      <span class="st-note" style="margin-left:8px;color:#0055d4">{{ $t('stats.teamsHint') }}</span>
    </h2>
    <table class="st-table">
      <thead>
        <tr>
          <th>{{ $t('stats.team') }}</th>
          <th>{{ $t('stats.campaigns') }}</th>
          <th>{{ $t('stats.sent') }}</th>
          <th>{{ $t('stats.click') }} ▼ <stats-help field="click" /></th>
          <th>{{ $t('stats.perCampaign') }}</th>
          <th>{{ $t('stats.open') }} <stats-help field="open" /></th>
          <th>{{ $t('stats.bounce') }} (2%) <stats-help field="bounce" /></th>
          <th>{{ $t('stats.complaint') }} (0.1%) <stats-help field="complaint" /></th>
        </tr>
      </thead>
      <tbody>
        <template v-for="r in rows">
          <tr v-if="r.empty" :key="r.tag" class="nosend" :data-cy="`team-row-${r.tag}`">
            <td>{{ r.name }}</td>
            <td colspan="7">
              <span class="st-tag dash">{{ $t('stats.noSend') }}</span>
              {{ $t('stats.noSendText') }}<template v-if="r.lists !== null"> · {{ $t('stats.listsN', { n: r.lists }) }}</template>
            </td>
          </tr>
          <tr v-else :key="r.tag" class="click" :class="{ sel: selected === r.tag, mute: !r.a.enough }"
            :data-cy="`team-row-${r.tag}`" @click="$emit('select', selected === r.tag ? '' : r.tag)">
            <td>
              <b v-if="r.tag === myTag">{{ r.name }}</b><template v-else>{{ r.name }}</template>
              <span v-if="r.tag === myTag" class="st-pill">{{ $t('stats.myTeam') }}</span>
              <span v-if="!r.a.enough" class="st-tag dash">{{ $t('stats.small') }} · {{ $t('stats.sent') }} {{ n(r.a.sent) }}</span>
              <span v-if="selected === r.tag" class="st-tag dash">✓ {{ $t('stats.shownBelow') }}</span>
            </td>
            <td>{{ r.a.n }}<span class="s">{{ $t('stats.rateCount', { n: r.a.nRate }) }}{{ excluded(r.a) }}</span></td>
            <td>{{ n(r.a.sent) }}</td>
            <template v-if="r.a.enough">
              <td>
                <span class="st-rbv"><b>{{ pt(r.a.click) }}</b>
                  <span class="st-bar"><i :style="{ width: w(r.a.click) }" /><u :style="{ left: w(all.click) }" /></span></span>
                <span class="s">{{ n(r.a.clickers) }}{{ $t('stats.people') }} · <span :class="chgClass(r, 'click')">{{ prevText(r, 'click') }}</span></span>
              </td>
              <td>
<svg v-if="spark(r).length" :width="spark(r).length * 10" height="24" style="display:block;margin-left:auto">
                  <title>{{ $t('stats.perCampaignTitle') }}</title>
                  <rect v-for="(b, i) in spark(r)" :key="i" :x="i * 10" :y="24 - b" width="8" :height="b"
                    :fill="i === spark(r).length - 1 ? '#0055d4' : '#aab4c5'" />
                </svg>
</td>
              <td>{{ pt(r.a.open) }}<span class="s">{{ n(r.a.viewers) }}{{ $t('stats.people') }} · <span :class="chgClass(r, 'open')">{{ prevText(r, 'open') }}</span></span></td>
              <td>
<span :class="{ 'st-over': over(r.a.bounce) }">{{ pt(r.a.bounce) }}<template v-if="over(r.a.bounce)"> ⚠</template></span>
                <span class="s">{{ n(r.a.bounces) }}{{ $t('stats.cases') }} · <span :class="chgClass(r, 'bounce')">{{ prevText(r, 'bounce') }}</span></span>
</td>
              <td>
<span :class="{ 'st-over': complaintOver(r.a.complaint) }">{{ pt(r.a.complaint, 2) }}<template v-if="complaintOver(r.a.complaint)"> ⚠</template></span>
                <span class="s">{{ n(r.a.complaints) }}{{ $t('stats.cases') }}</span>
</td>
            </template>
            <template v-else>
              <td>{{ pt(r.a.click) }}</td><td>—</td><td>{{ pt(r.a.open) }}</td><td>{{ pt(r.a.bounce) }}</td><td>{{ pt(r.a.complaint, 2) }}</td>
            </template>
          </tr>
        </template>
        <tr class="tot" data-cy="team-row-all">
          <td>{{ $t('stats.all') }}</td>
          <td>{{ all.n }}<span class="s">{{ $t('stats.rateCount', { n: all.nRate }) }}</span></td>
          <td>{{ n(all.sent) }}</td>
          <td>
<span class="st-rbv"><b>{{ pt(all.click) }}</b><span class="st-bar"><i style="background:#b9c1cf" :style="{ width: w(all.click) }" /><u :style="{ left: w(all.click) }" /></span></span>
            <span class="s">{{ n(all.clickers) }}{{ $t('stats.people') }}<template v-if="prevAll && prevAll.nRate > 0"> · <span :class="chgClassOf(all.click - prevAll.click, 'click')">{{ chgText(all.click - prevAll.click, 'click') }}</span></template></span>
</td>
          <td />
          <td>{{ pt(all.open) }}</td>
          <td><span :class="{ 'st-over': over(all.bounce) }">{{ pt(all.bounce) }}<template v-if="over(all.bounce)"> ⚠</template></span></td>
          <td>{{ pt(all.complaint, 2) }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script>
/* eslint-disable max-len, vue/max-len */
import StatsHelp from './StatsHelp.vue';
import {
  change, changeText, bounceOver, complaintOver, fmt, pctText, clickRate, classify, NO_TEAM_KEY,
} from '../stats';

export default {
  name: 'StatsTeamTable',
  components: { StatsHelp },
  props: {
    rows: { type: Array, required: true },
    all: { type: Object, required: true },
    prevByTeam: { type: Object, default: () => ({}) },
    prevAll: { type: Object, default: null },
    myTag: { type: String, default: '' },
    selected: { type: String, default: '' },
    now: { type: Date, default: () => new Date() },
  },
  data() { return { NO_TEAM_KEY }; },
  methods: {
    n: fmt,
    pt: pctText,
    over: bounceOver,
    complaintOver,
    w(v) { return `${Math.min(100, (v / 12) * 100)}%`; },
    excluded(a) {
      const x = [a.collecting ? this.$t('stats.collectingN', { n: a.collecting }) : '', a.untracked ? this.$t('stats.untrackedN', { n: a.untracked }) : ''].filter(Boolean);
      return x.length ? ` · ${x.join(' · ')} ${this.$t('stats.excluded')}` : '';
    },
    // Bars of the click rates of the team's campaigns, oldest first, 0% at the bottom (14% = full).
    spark(r) {
      return r.camps.filter((c) => classify(c, this.now).rateOk)
        .sort((x, y) => new Date(x.startedAt) - new Date(y.startedAt))
        .map((c) => Math.max(1, Math.min(24, (clickRate(c) / 14) * 24)));
    },
    prev(r) { const p = this.prevByTeam[r.tag]; return p && p.nRate > 0 ? p : null; },
    chgOf(r, kind) {
      const p = this.prev(r);
      return p ? change(r.a[kind] - p[kind], kind) : null;
    },
    prevText(r, kind) {
      const c = this.chgOf(r, kind);
      if (c) {
        return `${this.$t('stats.prev')} ${changeText(c)}`;
      }
      return kind === 'click' ? this.$t('stats.noPrev') : '';
    },
    chgClass(r, kind) { const c = this.chgOf(r, kind); return ['st-chg', c && c.good ? 'good' : '']; },
    chgText(d, kind) { return `${this.$t('stats.prev')} ${changeText(change(d, kind))}`; },
    chgClassOf(d, kind) { return ['st-chg', change(d, kind).good ? 'good' : '']; },
  },
};
</script>
