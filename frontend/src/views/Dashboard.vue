<template>
  <section class="dashboard content st">
    <header>
      <h1 class="title is-5 mb-1">{{ $t('stats.title') }}</h1>
      <div v-if="asOf" class="st-note">{{ $t('stats.asOf', { at: asOf }) }}</div>
    </header>

    <div class="is-flex is-align-items-center my-3 py-2" style="gap:14px;border-top:1px solid #e5e7eb;border-bottom:1px solid #e5e7eb">
      <span class="st-note">{{ $t('stats.period') }}</span>
      <span class="st-seg" :style="{ opacity: canStats ? 1 : 0.35 }" data-cy="period">
        <button v-for="p in periods" :key="String(p)" type="button" :class="{ on: period === p }"
          :disabled="!canStats" :data-cy="`period-${p === null ? 'all' : p}`" @click="setPeriod(p)">
          {{ p === null ? $t('stats.periodAll') : p === 365 ? $t('stats.periodYear') : $t('stats.periodDays', { n: p }) }}
        </button>
      </span>
      <span class="st-note">{{ rangeText }}</span>
      <a v-if="canStats" class="is-size-7" style="margin-left:auto" href="#" data-cy="defs-link"
        @click.prevent="openDefs">{{ $t('stats.defsLink') }}</a>
      <span v-else class="is-size-7 has-text-grey-light" style="margin-left:auto" aria-disabled="true"
        data-cy="defs-link">{{ $t('stats.defsLink') }}</span>
    </div>

    <template v-if="canStats">
      <b-loading v-if="loading" active :is-full-page="false" />
      <div v-if="error" class="notification is-danger is-light">{{ error }}</div>

      <template v-if="loaded && !loading">
        <div v-if="summary" class="st-sum" data-cy="stats-summary">{{ summary }}</div>
        <stats-team-table :rows="rows" :all="all" :prev-by-team="prevByTeam" :prev-all="prevAll" :my-tag="myTeam"
          :previous-error="prevError" :selected="team" :now="now" @select="setTeam" />
        <stats-campaign-table :camps="teamCamps" :all="camps" :team="team" :my-team="myTeam" :limit="limit" :now="now" @clear="setTeam('')" @more="limit += 20" />
        <stats-insights :camps="teamCamps" :age="age" :country="country" :overlap="overlap" :overlap-error="overlapError"
          :team-key="team" :scope="scopeText" :now="now" />
      </template>
    </template>
    <template v-else>
      <div class="notification is-light" data-cy="stats-noperm">{{ $t('stats.noPerm') }}</div>
      <div v-if="canCounts" class="st-card" data-cy="counts-summary">
        <h2>{{ $t('stats.status') }} <small>{{ $t('stats.statusAllTime') }}</small></h2>
        <div v-if="countsFailed" class="px-4 py-3 st-note" data-cy="counts-failed">{{ $t('stats.countsFailed') }}</div>
        <div v-else-if="!isCountsLoading" class="is-flex is-flex-wrap-wrap px-4 py-3" style="gap:30px">
          <div v-for="item in countSummary" :key="item.label">
            <strong class="is-size-5">{{ formatCount(item.total) }}</strong><br />{{ item.label }}
          </div>
        </div>
      </div>
    </template>

    <div v-if="canStats" class="st-fold" data-cy="counts-toggle" tabindex="0" role="button"
      @click="showCounts = !showCounts" @keyup.enter="showCounts = !showCounts">
      ④ {{ $t('stats.status') }} <i class="st-tri" :class="showCounts ? 'down' : 'right'" />
      <span v-if="countsFailed" class="st-note">{{ $t('stats.countsFailed') }}</span>
      <span v-else class="st-note">{{ $t('stats.statusNote', {
 lists: counts.lists.total || 0, subs: counts.subscribers.total || 0, camps: counts.campaigns.total || 0, msgs: counts.messages || 0,
}) }}</span>
    </div>
    <section v-if="canStats" v-show="showCounts" class="counts wrap">
      <div v-if="countsFailed" class="notification is-warning is-light" data-cy="counts-failed-section">{{ $t('stats.countsFailed') }}</div>
      <div v-else class="tile is-ancestor">
        <div class="tile is-vertical is-12">
          <div class="tile">
            <div class="tile is-parent is-vertical relative">
              <b-loading v-if="isCountsLoading" active :is-full-page="false" />
              <article class="tile is-child notification" data-cy="lists">
                <div class="columns is-mobile">
                  <div class="column is-6">
                    <p class="title">
                      <b-icon icon="format-list-bulleted-square" />
                      {{ $utils.niceNumber(counts.lists.total) }}
                    </p>
                    <p class="is-size-6 has-text-grey">
                      {{ $tc('globals.terms.list', counts.lists.total) }}
                    </p>
                  </div>
                  <div class="column is-6">
                    <ul class="no has-text-grey">
                      <li>
                        <label for="#">{{ $utils.niceNumber(counts.lists.public) }}</label>
                        {{ $t('lists.types.public') }}
                      </li>
                      <li>
                        <label for="#">{{ $utils.niceNumber(counts.lists.private) }}</label>
                        {{ $t('lists.types.private') }}
                      </li>
                      <li>
                        <label for="#">{{ $utils.niceNumber(counts.lists.optinSingle) }}</label>
                        {{ $t('lists.optins.single') }}
                      </li>
                      <li>
                        <label for="#">{{ $utils.niceNumber(counts.lists.optinDouble) }}</label>
                        {{ $t('lists.optins.double') }}
                      </li>
                    </ul>
                  </div>
                </div>
              </article><!-- lists -->

              <article class="tile is-child notification" data-cy="campaigns">
                <div class="columns is-mobile">
                  <div class="column is-6">
                    <p class="title">
                      <b-icon icon="rocket-launch-outline" />
                      {{ $utils.niceNumber(counts.campaigns.total) }}
                    </p>
                    <p class="is-size-6 has-text-grey">
                      {{ $tc('globals.terms.campaign', counts.campaigns.total) }}
                    </p>
                  </div>
                  <div class="column is-6">
                    <ul class="no has-text-grey">
                      <li v-for="(num, status) in counts.campaigns.byStatus" :key="status">
                        <label for="#" :data-cy="`campaigns-${status}`">{{ num }}</label>
                        {{ $t(`campaigns.status.${status}`) }}
                        <span v-if="status === 'running'" class="spinner is-tiny">
                          <b-loading :is-full-page="false" active />
                        </span>
                      </li>
                    </ul>
                  </div>
                </div>
              </article><!-- campaigns -->
            </div><!-- block -->

            <div class="tile is-parent relative">
              <b-loading v-if="isCountsLoading" active :is-full-page="false" />
              <article class="tile is-child notification" data-cy="subscribers">
                <div class="columns is-mobile">
                  <div class="column is-6">
                    <p class="title">
                      <b-icon icon="account-multiple" />
                      {{ $utils.niceNumber(counts.subscribers.total) }}
                    </p>
                    <p class="is-size-6 has-text-grey">
                      {{ $tc('globals.terms.subscriber', counts.subscribers.total) }}
                    </p>
                  </div>

                  <div class="column is-6">
                    <ul class="no has-text-grey">
                      <li>
                        <label for="#">{{ $utils.niceNumber(counts.subscribers.blocklisted) }}</label>
                        {{ $t('subscribers.status.blocklisted') }}
                      </li>
                      <li>
                        <label for="#">{{ $utils.niceNumber(counts.subscribers.orphans) }}</label>
                        {{ $t('dashboard.orphanSubs') }}
                      </li>
                    </ul>
                  </div><!-- subscriber breakdown -->
                </div><!-- subscriber columns -->
                <hr />
                <div class="columns" data-cy="messages">
                  <div class="column is-12">
                    <p class="title">
                      <b-icon icon="email-outline" />
                      {{ $utils.niceNumber(counts.messages) }}
                    </p>
                    <p class="is-size-6 has-text-grey">
                      {{ $t('dashboard.messagesSent') }}
                    </p>
                  </div>
                </div>
              </article><!-- subscribers -->
            </div>
          </div>
          <div class="tile is-parent relative">
            <b-loading v-if="isChartsLoading" active :is-full-page="false" />
          </div>
        </div>
      </div><!-- tile block -->
      <p v-if="settings['app.cache_slow_queries']" class="has-text-grey">
        *{{ $t('globals.messages.slowQueriesCached') }}
        <a href="https://listmonk.app/docs/maintenance/performance/" target="_blank" rel="noopener noreferer"
          class="has-text-grey">
          <b-icon icon="link-variant" /> {{ $t('globals.buttons.learnMore') }}
        </a>
      </p>
    </section>

    <stats-definitions v-if="canStats" ref="defs" v-model="defsOpen" />
  </section>
</template>
<script>
import Vue from 'vue';
import { mapState } from 'vuex';
import StatsTeamTable from '../components/StatsTeamTable.vue';
import StatsCampaignTable from '../components/StatsCampaignTable.vue';
import StatsInsights from '../components/StatsInsights.vue';
import StatsDefinitions from '../components/StatsDefinitions.vue';
import {
  BASELINE, MIN, PERIODS, PERIOD_DEFAULT,
} from '../stats-definitions';
import {
  aggregate, teamRows, campaignTeams, formatKST, fmt, statsNow, teamLabel, pctText, signedPoints, bounceOver, previousStatus,
} from '../stats';

const DAY = 86400000;

const DASHBOARD_PERMS = [
  'lists:get_all', 'lists:manage_all', 'subscribers:get_all',
  'campaigns:get_all', 'campaigns:manage_all', 'campaigns:get_analytics',
];

// The dashboard (LST-64): team comparison, the recent campaigns, hints for the next send and
// the old counts folded at the bottom. The numbers are the results of every team (the
// server hides lists, members and addresses of other teams, not the results).
export default Vue.extend({
  components: {
    StatsTeamTable, StatsCampaignTable, StatsInsights, StatsDefinitions,
  },
  data() {
    const queryPeriod = this.$route.query.period;
    const selected = queryPeriod === 'all' ? null : Number(queryPeriod);
    return {
      periods: PERIODS,
      period: PERIODS.includes(selected) ? selected : PERIOD_DEFAULT,
      team: this.$route.query.team || '',
      limit: 6,
      loading: false,
      loaded: false,
      error: '',
      now: new Date(),
      generatedAt: null,
      camps: [],
      prevCamps: null,
      prevError: false,
      requestID: 0,
      teams: [],
      myTeam: '',
      age: [],
      country: [],
      overlap: null,
      overlapError: false,
      showCounts: false,
      defsOpen: false,
      isCountsLoading: true,
      countsFailed: false,
      counts: {
        lists: {}, subscribers: {}, campaigns: {}, messages: 0,
      },
    };
  },
  computed: {
    ...mapState(['settings']),
    canStats() { return this.$can('campaigns:get_analytics'); },
    // The counts are company-wide: only roles that read across lists, subscribers or campaigns
    // get them. The server refuses the others (cmd/axm_dashboard.go dashboardPerms, same list).
    canCounts() { return this.$can(...DASHBOARD_PERMS); },
    countSummary() {
      return [
        { total: this.counts.lists.total || 0, label: this.$tc('globals.terms.list', 2) },
        { total: this.counts.subscribers.total || 0, label: this.$tc('globals.terms.subscriber', 2) },
        { total: this.counts.campaigns.total || 0, label: this.$tc('globals.terms.campaign', 2) },
        { total: this.counts.messages || 0, label: this.$t('dashboard.messagesSent') },
      ];
    },
    range() {
      const to = statsNow(this.now);
      const from = this.period === null ? null : new Date(to.getTime() - this.period * DAY);
      const prevFrom = from ? new Date(from.getTime() - this.period * DAY) : null;
      return { from, to, prevFrom };
    },
    rangeText() {
      if (!this.range.from) return '';
      const f = (d) => formatKST(d.toISOString()).slice(0, 5);
      return `${f(this.range.from)} ~ ${f(this.now)}`;
    },
    asOf() {
      const at = this.canStats ? this.generatedAt : this.counts.updatedAt;
      return at ? formatKST(at, this.$i18n.locale) : '';
    },
    rows() { return teamRows(this.camps, this.teams, this.now); },
    all() { return aggregate(this.camps, this.now); },
    prevByTeam() {
      return this.prevCamps ? Object.fromEntries(teamRows(this.prevCamps, [], this.now).map((r) => [r.tag, r.a])) : {};
    },
    prevAll() { return this.prevCamps ? aggregate(this.prevCamps, this.now) : null; },
    teamCamps() { return this.team ? this.camps.filter((c) => campaignTeams(c).includes(this.team)) : this.camps; },
    summary() {
      const a = this.rows.find((r) => r.tag === this.myTeam)?.a;
      if (!a?.enough) return '';
      const messages = [this.$t('stats.summaryCompany', {
        team: teamLabel(this.myTeam, this.$t.bind(this)),
        rate: pctText(a.click),
        all: pctText(this.all.click),
        delta: signedPoints(a.click - this.all.click),
      })];
      const prev = this.prevByTeam[this.myTeam];
      if (prev?.enough) {
        messages.push(this.$t('stats.summaryPrevious', { rate: pctText(prev.click), delta: signedPoints(a.click - prev.click) }));
      } else if (this.prevError || this.prevCamps) {
        const reason = previousStatus(prev, this.prevError);
        messages.push(this.$t(`stats.${reason}`));
      }
      if (a.sBounce >= MIN.groupSent) {
        const over = bounceOver(a.bounces, a.sBounce, MIN.groupSent);
        messages.push(this.$t(over ? 'stats.summaryBounceOver' : 'stats.summaryBounceWithin', { rate: pctText(a.bounce), line: BASELINE.bounce }));
      }
      return messages.join(' ');
    },
    scopeText() {
      return this.team ? this.$t('stats.scopeTeam', { team: teamLabel(this.team, this.$t.bind(this)) }) : this.$t('stats.scopeAll');
    },
  },
  methods: {
    formatCount: fmt,
    setPeriod(p) {
      if (this.period === p) return;
      const query = { ...this.$route.query };
      if (p === PERIOD_DEFAULT) {
        delete query.period;
      } else {
        query.period = p === null ? 'all' : String(p);
      }
      this.$router.replace({ query }).catch(() => {});
    },
    setTeam(tag) {
      this.team = tag;
      this.limit = 6;
      const query = { ...this.$route.query };
      if (tag) {
        query.team = tag;
      } else {
        delete query.team;
      }
      this.$router.replace({ query }).catch(() => {});
    },
    openDefs() {
      this.defsOpen = true;
      this.$nextTick(() => { if (this.$refs.defs) { this.$refs.defs.$el.scrollIntoView({ behavior: 'smooth' }); } });
    },
    fetchStats() {
      if (!this.canStats) {
        return;
      }
      this.now = new Date();
      this.loading = true;
      this.error = '';
      this.requestID += 1;
      const { requestID } = this;
      const { from, to, prevFrom } = this.range;
      const q = (f, t) => ({ from: f ? f.toISOString() : 'all', to: t.toISOString() });
      const params = q(from, to);
      this.prevCamps = null;
      this.prevError = false;
      this.age = [];
      this.country = [];
      this.overlap = null;
      this.overlapError = false;
      if (prevFrom) {
        this.$api.getStatsCampaigns(q(prevFrom, from)).then((previous) => {
          if (requestID === this.requestID) this.prevCamps = previous.campaigns;
        }).catch(() => {
          if (requestID === this.requestID) this.prevError = true;
        });
      }
      this.$api.getStatsCampaigns(params).then((cur) => {
        if (requestID !== this.requestID) return;
        this.camps = cur.campaigns;
        this.teams = cur.teams;
        this.myTeam = cur.myTeam;
        this.generatedAt = cur.generatedAt;
        this.loaded = true;
        this.fetchInsightData(params, requestID);
      }).catch((e) => {
        if (requestID === this.requestID) {
          this.loaded = false;
          this.error = (e && e.message) || String(e);
        }
      }).finally(() => { if (requestID === this.requestID) this.loading = false; });
    },
    fetchInsightData(params, requestID) {
      ['age', 'country'].forEach((by) => {
        this.$api.getStatsRecipients({ ...params, by }).then((rows) => {
          if (requestID === this.requestID) this[by] = rows;
        }).catch(() => {});
      });
      this.$api.getStatsCampaigns({ ...params, overlap: true }).then((rows) => {
        if (requestID === this.requestID) this.overlap = rows;
      }).catch(() => {
        if (requestID === this.requestID) this.overlapError = true;
      });
    },
    fetchCounts() {
      if (!this.canCounts) {
        this.isCountsLoading = false;
        return;
      }
      this.isCountsLoading = true;
      this.countsFailed = false;
      this.$api.getDashboardCounts().then((data) => {
        this.counts = data;
        this.isCountsLoading = false;
      }).catch(() => {
        // Not zeros: the initial values would read as "nothing exists".
        this.countsFailed = true;
        this.isCountsLoading = false;
      });
    },
    fetchData() {
      this.fetchCounts();
      this.fetchStats();
    },
  },
  created() {
    this.$root.$on('page.refresh', this.fetchData);
  },
  destroyed() {
    this.$root.$off('page.refresh', this.fetchData);
  },
  mounted() {
    this.fetchData();
  },
});
</script>
