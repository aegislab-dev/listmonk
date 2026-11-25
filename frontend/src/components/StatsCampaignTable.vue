<template>
  <!-- eslint-disable vue/max-len -->
  <div class="st-card" data-cy="stats-campaigns">
    <h2>
      ② {{ $t('stats.recent') }}
      <span v-if="team" class="st-tag" data-cy="team-filter">{{ $t('stats.team') }}: {{ teamName }}
        <a href="#" @click.prevent="$emit('clear')" @keyup.enter="$emit('clear')" :title="$t('globals.buttons.clear')">×</a></span>
      <small>{{ team ? $t('stats.recentTeamNote', { total: total, shown: shown.length }) : $t('stats.recentNote', { n: shown.length }) }}</small>
    </h2>
    <table class="st-table">
      <thead>
        <tr>
          <th>{{ $tc('globals.terms.campaign', 1) }}</th><th>{{ $t('stats.start') }}</th><th>{{ $t('stats.sent') }}</th>
          <th>{{ $t('stats.click') }} <stats-help field="click" /></th><th>{{ $t('stats.open') }}</th>
          <th>{{ $t('stats.bounce') }}</th><th>{{ $t('stats.complaint') }}</th>
          <th>{{ $t('stats.vsTeam') }} <stats-help field="median" /></th><th>{{ $t('stats.open2') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="c in shown" :key="c.id" :class="{ coll: k(c).collecting || sum(c).small }" :data-cy="`camp-row-${c.id}`" :data-own="String(c.own)">
          <td>
            <b>#{{ c.id }}</b> {{ c.name }}
            <span v-if="c.subject" class="s" data-cy="campaign-subject">{{ c.subject }}</span>
            <span v-if="isMyTeam(c)" class="st-pill">{{ $t('stats.myTeam') }}</span>
            <span v-else class="st-tag">{{ teamsText(c) }}</span>
            <span v-if="c.teamTags && c.teamTags.length > 1" class="st-tag">{{ $t('stats.sharedTeams', { n: c.teamTags.length }) }}</span>
            <span v-if="!c.own" class="s">{{ $t('stats.campaignLists', { n: c.listsN }) }}</span>
            <span v-if="k(c).state === 'running'" class="st-tag">{{ $t('stats.running') }}</span>
            <span v-else-if="k(c).state === 'paused' || k(c).state === 'cancelled'" class="st-tag">{{ $t(`campaigns.status.${k(c).state}`) }}</span>
            <span v-else-if="k(c).collecting" class="st-tag">{{ $t('stats.collecting', { h: k(c).hours }) }}</span>
            <span v-else-if="k(c).untracked" class="st-tag">{{ $t('stats.untracked') }}</span>
            <span v-else-if="c.sent < MIN.campaignSent" class="st-tag dash">{{ $t('stats.small') }} · {{ $t('stats.sent') }} {{ c.sent }}</span>
          </td>
          <td style="white-space:nowrap">{{ kst(c.startedAt) }}</td>
          <td style="white-space:nowrap">{{ n(c.sent) }}<template v-if="k(c).state === 'running'"> / {{ n(c.toSend) }}</template></td>
          <td v-if="k(c).state === 'running'" colspan="5" style="text-align:center;color:#a2a8b2">{{ $t('stats.runningNote') }}</td>
          <template v-else>
            <td>
<template v-if="k(c).untracked">{{ $t('stats.untracked') }}<span class="s">{{ $t('stats.notZero') }}</span></template>
              <template v-else><b>{{ pt(sum(c).click) }}</b><span class="s">{{ n(c.clickers) }}{{ $t('stats.people') }}</span></template>
</td>
            <td>
<template v-if="k(c).untracked">{{ $t('stats.untracked') }}</template>
              <template v-else>{{ pt(sum(c).open) }}<span class="s">{{ n(c.viewers) }}{{ $t('stats.people') }}</span></template>
</td>
            <td><span :class="{ 'st-over': sum(c).bounceWarning }">{{ pt(sum(c).bounce) }}<template v-if="sum(c).bounceWarning"> ⚠</template></span><span class="s">{{ c.hard + c.soft }}{{ $t('stats.cases') }}</span></td>
            <td><span :class="{ 'st-over': sum(c).complaintWarning }">{{ pt(sum(c).complaint, 2) }}<template v-if="sum(c).complaintWarning"> ⚠</template></span><span class="s">{{ c.complaint }}{{ $t('stats.cases') }}</span></td>
            <td>
              <span v-if="k(c).collecting || c.sent < MIN.campaignSent" class="st-note">{{ $t('stats.noCompare') }}</span>
              <span v-else-if="k(c).untracked" />
              <template v-else-if="sum(c).median">
                <span :class="['st-chg', sum(c).cmp.good ? 'good' : '']">{{ changeText(sum(c).cmp) }}</span>
                <span class="s">{{ comparisonName(sum(c)) }} {{ $t('stats.median') }} {{ pt(sum(c).median.value) }} ({{ sum(c).median.n }}{{ $t('stats.cases2') }})</span>
              </template>
              <span v-else class="st-note">{{ $t('stats.noCompare') }}</span>
            </td>
          </template>
          <td>
            <stats-campaign-actions v-if="c.own" :campaign="c" show-open />
            <span v-else class="st-note">{{ $t('stats.noOpen') }}</span>
          </td>
        </tr>
      </tbody>
    </table>
    <div v-if="hidden > 0" class="st-fold" style="border:0;border-top:1px solid #e5e7eb;border-radius:0 0 8px 8px;margin:0" data-cy="camp-more" tabindex="0" role="button"
      @click="$emit('more')" @keyup.enter="$emit('more')">
      {{ $t('stats.more', { n: hidden }) }} <i class="st-tri down" />
    </div>
  </div>
</template>

<script>
/* eslint-disable max-len, vue/max-len */
import StatsHelp from './StatsHelp.vue';
import StatsCampaignActions from './StatsCampaignActions.vue';
import {
  campaignSummary, campaignTeams, changeText, formatKST, fmt, pctText, teamLabel,
} from '../stats';
import { MIN } from '../stats-definitions';

export default {
  name: 'StatsCampaignTable',
  components: { StatsHelp, StatsCampaignActions },
  props: {
    camps: { type: Array, required: true },
    all: { type: Array, required: true },
    team: { type: String, default: '' },
    myTeam: { type: String, default: '' },
    limit: { type: Number, default: 6 },
    now: { type: Date, default: () => new Date() },
  },
  data() { return { MIN }; },
  computed: {
    sorted() { return [...this.camps].sort((a, b) => new Date(b.startedAt) - new Date(a.startedAt) || b.id - a.id); },
    shown() { return this.sorted.slice(0, this.limit); },
    total() { return this.sorted.length; },
    hidden() { return this.sorted.length - this.shown.length; },
    teamName() { return this.label(this.team); },
    summaries() { return new Map(this.shown.map((c) => [c.id, campaignSummary(c, this.all, this.now)])); },
  },
  methods: {
    n: fmt,
    pt: pctText,
    kst(at) { return formatKST(at, this.$i18n.locale); },
    changeText(c) { return changeText(c, this.$t.bind(this)); },
    k(c) { return this.sum(c).k; },
    sum(c) { return this.summaries.get(c.id); },
    label(tag) { return teamLabel(tag, this.$t.bind(this)); },
    comparisonName(summary) { return summary.team ? this.label(summary.team) : this.$t('stats.all'); },
    isMyTeam(c) { return !!this.myTeam && campaignTeams(c).includes(this.myTeam); },
    teamsText(c) { return campaignTeams(c).map(this.label).join(', '); },
  },
};
</script>
