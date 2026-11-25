<template>
  <!-- eslint-disable vue/max-len -->
  <div v-if="summary" class="st st-resultbox" data-cy="result-summary">
    <div style="display:flex;align-items:center;gap:10px;flex-wrap:wrap">
      <b>{{ $t('stats.resultSummary') }}</b>
      <span v-if="summary.k.collecting" class="st-tag" data-cy="result-collecting">{{ $t('stats.collectingLong', { h: summary.k.hours }) }}</span>
      <span v-else-if="summary.k.state === 'running'" class="st-tag">{{ $t('stats.running') }}</span>
      <span class="st-note">{{ kst(camp.startedAt) }} {{ $t('stats.startedAt') }} · {{ summary.teamName }}</span>
      <span style="margin-left:auto" class="st-note">
        <router-link v-if="showLinks" :to="{ name: 'campaignAnalytics', query: { id: camp.id } }" data-cy="result-analytics">{{ $t('stats.analyze') }}</router-link>
      </span>
    </div>
    <div class="st-ms" style="margin-top:10px">
      <div class="st-m" :class="{ mute: dim }"><div class="l">{{ $t('stats.sent') }}</div><div class="v">{{ n(camp.sent) }}</div><div class="p">{{ $t('stats.target') }} {{ n(camp.toSend) }}</div></div>
      <div class="st-m hero" :class="{ mute: dim }" data-cy="result-click">
        <div class="l">{{ $t('stats.click') }} <stats-help field="click" /></div>
        <template v-if="summary.k.untracked"><div class="v" style="font-size:16px">{{ $t('stats.untracked') }}</div><div class="p">{{ $t('stats.notZero') }}</div></template>
        <template v-else>
          <div class="v">{{ pt(summary.click) }}</div><div class="p">{{ n(camp.clickers) }}{{ $t('stats.people') }}</div>
          <div class="p" data-cy="result-compare">
            <template v-if="summary.cmp"><span :class="['st-chg', summary.cmp.good ? 'good' : '']">{{ changeText(summary.cmp) }}</span> {{ summary.teamName }} {{ $t('stats.median') }} {{ pt(summary.median.value) }} ({{ summary.median.n }}{{ $t('stats.cases2') }})</template>
            <template v-else-if="dim">{{ $t('stats.noCompare') }}</template>
            <template v-else>{{ summary.teamName }} {{ $t('stats.small') }}</template>
          </div>
        </template>
      </div>
      <div class="st-m" :class="{ mute: dim }">
<div class="l">{{ $t('stats.open') }} <stats-help field="open" /></div>
        <div class="v"><template v-if="summary.k.untracked" /><template v-else>{{ pt(summary.open) }}</template></div><div class="p">{{ summary.k.untracked ? $t('stats.untracked') : `${n(camp.viewers)}${$t('stats.people')}` }}</div>
</div>
      <div class="st-m" :class="{ mute: dim }">
<div class="l">{{ $t('stats.bounce') }} <stats-help field="bounce" /></div>
        <div class="v"><span :class="{ 'st-over': over(summary.bounce) }">{{ pt(summary.bounce) }}<template v-if="over(summary.bounce)"> ⚠</template></span></div>
        <div class="p">{{ camp.hard + camp.soft }}{{ $t('stats.cases') }} · {{ $t('stats.line') }} 2%</div>
</div>
      <div class="st-m" :class="{ mute: dim }">
<div class="l">{{ $t('stats.complaint') }} <stats-help field="complaint" /></div>
        <div class="v"><span :class="{ 'st-over': complaintOver(summary.complaint) }">{{ pt(summary.complaint, 2) }}<template v-if="complaintOver(summary.complaint)"> ⚠</template></span></div>
        <div class="p">{{ camp.complaint }}{{ $t('stats.cases') }} · {{ $t('stats.line') }} 0.1%</div>
</div>
    </div>
    <div class="st-note" style="margin-top:8px">{{ $t('stats.proofNote', { bounce: camp.hard + camp.soft, complaint: camp.complaint }) }}</div>
  </div>
</template>

<script>
/* eslint-disable max-len, vue/max-len */
import StatsHelp from './StatsHelp.vue';
import {
  campaignSummary, changeText, formatKST, fmt, pctText, bounceOver, complaintOver,
} from '../stats';

// The result of one campaign (LST-64): the same figures as the dashboard row, read from the
// statistics API for the 90 days before the campaign and up to now (the team median needs the
// team's other campaigns).
export default {
  name: 'CampaignResultSummary',
  components: { StatsHelp },
  props: {
    campaignId: { type: Number, required: true },
    startedAt: { type: [String, Date], default: null },
    showLinks: { type: Boolean, default: false },
  },
  data() { return { camp: null, camps: [], now: new Date() }; },
  computed: {
    summary() { return this.camp ? campaignSummary(this.camp, this.camps, this.now) : null; },
    dim() { return !!this.summary && (this.summary.k.collecting || this.summary.k.state === 'running'); },
  },
  watch: { campaignId() { this.load(); }, startedAt() { this.load(); } },
  mounted() { this.load(); },
  methods: {
    n: fmt,
    pt: pctText,
    over: bounceOver,
    complaintOver,
    changeText,
    kst: formatKST,
    load() {
      this.camp = null;
      if (!this.startedAt || !this.$can('campaigns:get_analytics')) {
        return;
      }
      const start = new Date(this.startedAt);
      const from = new Date(start.getTime() - 90 * 86400000);
      const to = new Date(Date.now() + 86400000);
      if (to - from > 780 * 86400000) {
        return;
      }
      this.$api.getStatsCampaigns({ from: from.toISOString(), to: to.toISOString() }).then((d) => {
        this.now = new Date();
        this.camps = d.campaigns;
        this.camp = d.campaigns.find((c) => c.id === this.campaignId) || null;
      }).catch(() => {});
    },
  },
};
</script>
