<template>
  <!-- eslint-disable vue/max-len -->
  <div class="st-card" data-cy="stats-campaigns">
    <h2>
      ② {{ $t('stats.recent') }}
      <span v-if="team" class="st-tag" data-cy="team-filter">{{ $t('stats.team') }}: {{ teamName }}
        <a href="#" @click.prevent="$emit('clear')" @keyup.enter="$emit('clear')" :title="$t('globals.buttons.clear')">✕</a></span>
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
        <tr v-for="c in shown" :key="c.id" :class="{ coll: k(c).collecting }" :data-cy="`camp-row-${c.id}`" :data-own="String(c.own)">
          <td>
            <b>#{{ c.id }}</b> {{ c.name }}
            <span v-if="c.own" class="st-pill">{{ $t('stats.myTeam') }}</span>
            <span v-else class="st-tag">{{ teamsText(c) }}</span>
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
              <template v-else><b>{{ pt(rate(c).click) }}</b><span class="s">{{ n(c.clickers) }}{{ $t('stats.people') }}</span></template>
</td>
            <td>
<template v-if="k(c).untracked">{{ $t('stats.untracked') }}</template>
              <template v-else>{{ pt(rate(c).open) }}<span class="s">{{ n(c.viewers) }}{{ $t('stats.people') }}</span></template>
</td>
            <td><span :class="{ 'st-over': over(rate(c).bounce) }">{{ pt(rate(c).bounce) }}<template v-if="over(rate(c).bounce)"> ⚠</template></span><span class="s">{{ c.hard + c.soft }}{{ $t('stats.cases') }}</span></td>
            <td><span :class="{ 'st-over': complaintOver(rate(c).complaint) }">{{ pt(rate(c).complaint, 2) }}<template v-if="complaintOver(rate(c).complaint)"> ⚠</template></span><span class="s">{{ c.complaint }}{{ $t('stats.cases') }}</span></td>
            <td style="white-space:nowrap">
              <span v-if="k(c).collecting || c.sent < MIN.campaignSent" class="st-note">{{ $t('stats.noCompare') }}</span>
              <span v-else-if="k(c).untracked" />
              <template v-else-if="sum(c).median">
                <span :class="['st-chg', sum(c).cmp.good ? 'good' : '']">{{ changeText(sum(c).cmp) }}</span>
                <span class="s">{{ sum(c).teamName }} {{ $t('stats.median') }} {{ pt(sum(c).median.value) }} ({{ sum(c).median.n }}{{ $t('stats.cases2') }})</span>
              </template>
              <span v-else class="st-note">{{ sum(c).teamName }} {{ $t('stats.small') }}</span>
            </td>
          </template>
          <td style="white-space:nowrap">
            <template v-if="c.own">
              <router-link :to="{ name: 'campaignAnalytics', query: { id: c.id } }">{{ $t('stats.analyze') }}</router-link>
              <router-link :to="{ name: 'campaign', params: { id: c.id } }" style="margin-left:8px">{{ $t('stats.openCamp') }}</router-link>
            </template>
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
import {
  campaignSummary, changeText, classify, complaintOver, bounceOver, formatKST, fmt, pctText, pct, teamLabel,
} from '../stats';
import { MIN } from '../stats-definitions';
import { teamName } from '../teams';

export default {
  name: 'StatsCampaignTable',
  components: { StatsHelp },
  props: {
    camps: { type: Array, required: true },
    all: { type: Array, required: true },
    team: { type: String, default: '' },
    limit: { type: Number, default: 6 },
    now: { type: Date, default: () => new Date() },
  },
  data() { return { MIN }; },
  computed: {
    sorted() { return [...this.camps].sort((a, b) => new Date(b.startedAt) - new Date(a.startedAt) || b.id - a.id); },
    shown() { return this.sorted.slice(0, this.limit); },
    total() { return this.sorted.length; },
    hidden() { return this.sorted.length - this.shown.length; },
    teamName() { return teamName(this.team); },
  },
  methods: {
    n: fmt,
    pt: pctText,
    over: bounceOver,
    complaintOver,
    changeText,
    kst: formatKST,
    k(c) { return classify(c, this.now); },
    sum(c) { return campaignSummary(c, this.all, this.now); },
    rate(c) {
      return {
        click: pct(c.clickers, c.sent), open: pct(c.viewers, c.sent), bounce: pct(c.hard + c.soft, c.sent), complaint: pct(c.complaint, c.sent, 2),
      };
    },
    teamsText(c) { return (c.teamTags && c.teamTags.length ? c.teamTags : ['__none__']).map(teamLabel).join(', '); },
  },
};
</script>
