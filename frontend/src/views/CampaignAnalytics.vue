<template>
  <section class="analytics content relative">
    <h1 class="title is-4">
      {{ $t('analytics.title') }}
    </h1>
    <div v-if="serverConfig.privacy.disable_tracking || !serverConfig.privacy.individual_tracking"
      class="notification is-info">
      <template v-if="serverConfig.privacy.disable_tracking">
        {{ $t('analytics.trackingDisabled') }}
      </template>
      <template v-else-if="!serverConfig.privacy.individual_tracking">
        {{ $t('analytics.nonIndividualTracking') }}
      </template>
    </div>
    <hr />

    <form @submit.prevent="onSubmit">
      <div class="columns">
        <div class="column is-6">
          <b-field :label="$t('globals.terms.campaigns')" label-position="on-border">
            <b-taginput v-model="form.campaigns" :data="queriedCampaigns" name="campaigns" ellipsis icon="tag-outline"
              :placeholder="$t('globals.terms.campaigns')" autocomplete :allow-new="false" :open-on-focus="true"
              :before-adding="isCampaignSelected" @typing="queryCampaigns" @focus="queryCampaigns" field="name"
              :loading="isSearchLoading" />
          </b-field>
        </div>

        <div class="column is-5">
          <div class="columns">
            <div class="column is-6">
              <b-field data-cy="from" :label="$t('analytics.fromDate')" label-position="on-border">
                <b-datetimepicker v-model="form.from" icon="calendar-clock" :timepicker="{ hourFormat: '24' }"
                  :datetime-formatter="formatDateTime" @input="onFromDateChange" />
              </b-field>
            </div>
            <div class="column is-6">
              <b-field data-cy="to" :label="$t('analytics.toDate')" label-position="on-border">
                <b-datetimepicker v-model="form.to" icon="calendar-clock" :timepicker="{ hourFormat: '24' }"
                  :datetime-formatter="formatDateTime" @input="onToDateChange" />
              </b-field>
            </div>
          </div><!-- columns -->
        </div><!-- columns -->

        <div class="column is-1">
          <b-button native-type="submit" type="is-primary" icon-left="magnify" :disabled="form.campaigns.length === 0"
            data-cy="btn-search" />
        </div>
      </div><!-- columns -->
    </form>

    <!-- LST-36: copy the counts of one campaign as a proof summary (block v1). -->
    <div class="proof-summary columns is-vcentered is-mobile mt-1 mb-0" data-cy="proof-summary">
      <div class="column is-narrow">
        <b-button icon-left="file-multiple-outline" :disabled="!!proofDisabledReason" :loading="proof.loading"
          data-cy="btn-proof-summary" @click="onProofSummary">
          {{ $t('analytics.proofSummary') }}
        </b-button>
      </div>
      <div class="column">
        <p class="is-size-7 has-text-grey" data-cy="proof-summary-help">
          <template v-if="proofDisabledReason === 'one'">{{ $t('analytics.proofSummaryOne') }}</template>
          <template v-else-if="proofDisabledReason === 'notSent'">{{ $t('analytics.proofSummaryNotSent') }}</template>
          <template v-else>{{ $t('analytics.proofSummaryScope') }}</template>
        </p>
      </div>
    </div>

    <b-modal scroll="keep" :aria-modal="true" :active.sync="proof.open" :width="680" has-modal-card>
      <div class="modal-card content proof-summary-modal" style="width: auto">
        <header class="modal-card-head">
          <h4>{{ $t('analytics.proofSummaryTitle') }}</h4>
        </header>
        <section class="modal-card-body">
          <p class="is-size-7 has-text-grey">{{ $t('analytics.proofSummaryModalHelp') }}</p>
          <textarea ref="proofText" class="textarea proof-summary-text" rows="10" readonly spellcheck="false"
            :aria-label="$t('analytics.proofSummaryTitle')"
            data-cy="proof-summary-text" :value="proof.text" @focus="$event.target.select()" />
          <p class="notification is-info is-light is-size-7 mt-3 mb-0">{{ $t('analytics.proofSummaryAsOf') }}</p>
        </section>
        <footer class="modal-card-foot">
          <b-button @click="proof.open = false">{{ $t('globals.buttons.close') }}</b-button>
          <b-button type="is-primary" icon-left="file-multiple-outline" data-cy="btn-proof-copy" @click="copyProof">
            {{ $t('globals.buttons.copy') }}
          </b-button>
        </footer>
      </div>
    </b-modal>

    <section class="charts mt-5">
      <div class="chart" v-for="(v, k) in charts" :key="k">
        <div class="columns">
          <div class="column is-9">
            <b-loading v-if="v.loading" :active="v.loading" :is-full-page="false" />
            <h4>
              {{ v.name }}
              <span v-if="v.type !== 'bar'" class="has-text-grey-light">({{ $utils.niceNumber(counts[k]) }})</span>
            </h4>
            <chart :type="v.type" v-if="!v.loading" :data="v.data" :on-click="v.onClick" />
          </div>
          <div class="column is-2 donut-container">
            <chart type="donut" v-if="!v.loading" :data="v.donutData" />
          </div>
        </div>
      </div>
    </section>
  </section>
</template>

<script>
import dayjs from 'dayjs';
import Vue from 'vue';
import { mapState } from 'vuex';
import { colors } from '../constants';
import Chart from '../components/Chart.vue';
import { buildProofSummaryV1, teamsOfCampaign, trackingStateFor } from '../proof-summary';

const chartColorRed = '#ee7d5b';
const chartColors = [
  colors.primary,
  '#FFB50D',
  '#41AC9C',
  chartColorRed,
  '#7FC7BC',
  '#3a82d6',
  '#688ED9',
  '#FFC43D',
];

export default Vue.extend({
  components: {
    Chart,
  },

  data() {
    return {
      isSearchLoading: false,
      queriedCampaigns: [],

      // Data for each view.
      counts: {
        views: 0,
        clicks: 0,
        bounces: 0,
        links: 0,
      },
      urls: [],
      charts: {
        views: {
          name: this.$t('campaigns.views'),
          type: 'line',
          data: null,
          fn: this.$api.getCampaignViewCounts,
          chartFn: this.makeCharts,
          loading: false,
        },

        clicks: {
          name: this.$t('campaigns.clicks'),
          type: 'line',
          data: null,
          fn: this.$api.getCampaignClickCounts,
          chartFn: this.makeCharts,
          loading: false,
        },

        bounces: {
          name: this.$t('globals.terms.bounces'),
          type: 'line',
          data: null,
          fn: this.$api.getCampaignBounceCounts,
          chartFn: this.makeCharts,
          donutColor: chartColorRed,
          loading: false,
        },

        links: {
          name: this.$t('analytics.links'),
          type: 'bar',
          data: null,
          loading: false,
          fn: this.$api.getCampaignLinkCounts,
          chartFn: this.makeLinksChart,
          onClick: this.onLinkClick,
        },
      },

      form: {
        campaigns: [],
        from: null,
        to: null,
      },

      // The proof summary (LST-36): the text of the modal and whether it is being made.
      proof: { open: false, loading: false, text: '' },
    };
  },

  methods: {
    onFromDateChange() {
      if (this.form.from > this.form.to) {
        this.form.to = dayjs(this.form.from).add(7, 'day').toDate();
      }
    },

    onToDateChange() {
      if (this.form.from > this.form.to) {
        this.form.from = dayjs(this.form.to).add(-7, 'day').toDate();
      }
    },

    formatDateTime(s) {
      return dayjs(s).format('YYYY-MM-DD HH:mm');
    },

    isCampaignSelected(camp) {
      return !this.form.campaigns.find(({ id }) => id === camp.id);
    },

    makeLinksChart(typ, camps, data) {
      const labels = data.map((l) => {
        try {
          this.urls.push(l.url);
          const u = new URL(l.url);
          if (l.url.length > 80) {
            return `${u.hostname}${u.pathname.substr(0, 50)}..`;
          }
          return u.hostname + u.pathname;
        } catch {
          return l.url;
        }
      });

      const out = {
        labels,
        datasets: [
          {
            data: data.map((l) => l.count),
            backgroundColor: chartColors,
          }],
      };

      return { points: out, donut: null };
    },

    makeCharts(typ, campaigns, data) {
      // Make a campaign id => camp lookup map to group incoming
      // data by campaigns.
      const camps = campaigns.reduce((obj, c) => {
        const out = { ...obj };
        out[c.id] = c;
        return out;
      }, {});
      const campIDs = Object.keys(camps);
      // datasets[] array for line chart.
      const lines = campIDs.map((id, n) => {
        const cId = parseInt(id, 10);
        const points = data.filter((item) => item.campaignId === cId);

        return {
          label: camps[id].name,
          data: points.map((item) => ({ x: this.formatDateTime(item.timestamp), y: item.count })),
          borderColor: chartColors[n % chartColors.length],
          borderWidth: 2,
          pointHoverBorderWidth: 5,
          pointBorderWidth: 0.5,
        };
      });

      // Donut.
      const labels = [];
      const points = campIDs.map((id) => {
        labels.push(camps[id].name);
        const cId = parseInt(id, 10);
        const sum = data.reduce((a, item) => (item.campaignId === cId ? a + item.count : a), 0);
        return sum;
      });

      const donut = {
        labels,
        datasets: [{
          data: points, backgroundColor: chartColors, borderWidth: 6,
        }],
      };
      return { points: { datasets: lines }, donut };
    },

    onSubmit() {
      this.$router.push({ query: { id: this.form.campaigns.map((c) => c.id), from: dayjs(this.form.from).unix(), to: dayjs(this.form.to).unix() } });
    },

    queryCampaigns(q) {
      this.isSearchLoading = true;
      this.$api.getCampaigns({
        query: q,
        order_by: 'created_at',
        order: 'DESC',
      }).then((data) => {
        this.isSearchLoading = false;
        this.queriedCampaigns = data.results.map((c) => {
          // Change the name to include the ID in the auto-suggest results.
          const camp = c;
          camp.name = `#${c.id}: ${c.name}`;
          return camp;
        });
      });
    },

    getData(typ, camps) {
      this.charts[typ].loading = true;
      // Call the HTTP API.
      this.charts[typ].fn({
        id: camps.map((c) => c.id),
        from: this.form.from,
        to: this.form.to,
      }).then((data) => {
        // Set the total count.
        this.counts[typ] = data.reduce((sum, d) => sum + d.count, 0);

        const { points, donut } = this.charts[typ].chartFn(typ, camps, data);
        this.charts[typ].data = points;
        this.charts[typ].donutData = donut;
        this.charts[typ].loading = false;
      });
    },

    // The proof summary of the one selected campaign (LST-36). The numbers are those of the
    // campaign's whole life (created .. now), not of the dates picked on this screen, and
    // they do not touch the charts above.
    async onProofSummary() {
      const sel = this.form.campaigns[0];
      if (this.proofDisabledReason || !sel || this.proof.loading) {
        return;
      }

      this.proof.loading = true;
      try {
        // The selector shows "#id: name" in place of the name, so ask for the campaign again.
        const camp = await this.$api.getCampaign(sel.id);
        if (['draft', 'scheduled'].includes(camp.status)) {
          this.$utils.toast(this.$t('analytics.proofSummaryNotSent'), 'is-danger');
          return;
        }

        const at = new Date(Math.floor(Date.now() / 1000) * 1000);
        const range = { id: [camp.id], from: new Date(camp.createdAt), to: at };
        const [views, clicks, bounces] = await Promise.all([
          this.$api.getCampaignViewCounts(range),
          this.$api.getCampaignClickCounts(range),
          this.$api.getCampaignBounceCounts(range),
        ]);
        const total = (rows) => rows.reduce((sum, d) => sum + d.count, 0);

        // The teams are the team tags of the campaign's lists; the lists this screen already
        // has carry the tags, any other list is asked for once.
        let known = (this.lists && this.lists.results) || [];
        if (camp.lists.some((l) => !known.find((k) => k.id === l.id))) {
          const all = await this.$api.queryLists({ minimal: true, per_page: 'all' });
          known = [...known, ...((all && all.results) || [])];
        }

        const sent = camp.sent || 0;
        const tracking = trackingStateFor({
          privacy: this.serverConfig.privacy,
          sent,
          viewsTotal: total(views),
          clicksTotal: total(clicks),
        });
        this.proof.text = buildProofSummaryV1({
          campaign: camp,
          teams: teamsOfCampaign(camp.lists, known),
          sent,
          views: total(views),
          clicks: total(clicks),
          bounces: total(bounces),
          tracking,
          at,
          rootURL: this.serverConfig.root_url,
        });
        this.proof.open = true;
        this.$nextTick(this.fitProofText);
      } catch (e) {
        // The HTTP layer has shown the error.
      } finally {
        this.proof.loading = false;
      }
    },

    // The text box is as tall as its text: the lines wrap on a narrow screen, and the copied
    // text must be seen to the last line.
    fitProofText() {
      const el = this.$refs.proofText;
      if (!el) {
        return;
      }
      el.style.height = 'auto';
      el.style.height = `${el.scrollHeight + 2}px`;
    },

    async copyProof() {
      let ok = false;
      try {
        if (navigator.clipboard && window.isSecureContext) {
          await navigator.clipboard.writeText(this.proof.text);
          ok = true;
        }
      } catch (e) {
        ok = false;
      }
      if (!ok) {
        const el = this.$refs.proofText;
        try {
          el.focus();
          el.select();
          ok = document.execCommand('copy');
        } catch (e) {
          ok = false;
        }
      }
      if (ok) {
        this.$utils.toast(this.$t('analytics.proofSummaryCopied'));
      } else {
        this.$utils.toast(this.$t('analytics.proofSummaryCopyFailed'), 'is-danger');
      }
    },

    onLinkClick(e) {
      const bars = e.chart.getElementsAtEventForMode(e, 'nearest', { intersect: true }, true);
      if (bars.length > 0) {
        window.open(this.urls[bars[0].index], '_blank', 'noopener noreferrer');
      }
    },
  },

  computed: {
    ...mapState(['serverConfig', 'lists']),

    // Why the proof summary button is off: '' (on), 'one' (not exactly one campaign) or
    // 'notSent' (a draft or a scheduled campaign has nothing to count yet).
    proofDisabledReason() {
      if (this.form.campaigns.length !== 1) {
        return 'one';
      }
      return ['draft', 'scheduled'].includes(this.form.campaigns[0].status) ? 'notSent' : '';
    },
  },

  created() {
    const now = dayjs().set('hour', 23).set('minute', 59).set('seconds', 0);
    const weekAgo = now.subtract(7, 'day').set('hour', 0).set('minute', 0);
    const from = this.$route.query.from ? dayjs.unix(this.$route.query.from) : weekAgo;
    const to = this.$route.query.to ? dayjs.unix(this.$route.query.to) : now;
    this.form.from = from.toDate();
    this.form.to = to.toDate();
  },

  mounted() {
    // Fetch one or more campaigns if there are ?id params, wait for the fetches
    // to finish, add them to the campaign selector and submit the form.
    const ids = this.$utils.parseQueryIDs(this.$route.query.id);
    if (ids.length > 0) {
      this.isSearchLoading = true;
      Promise.allSettled(ids.map((id) => this.$api.getCampaign(id))).then((data) => {
        data.forEach((d) => {
          if (d.status !== 'fulfilled') {
            return;
          }

          const camp = d.value;
          camp.name = `#${camp.id}: ${camp.name}`;
          this.form.campaigns.push(camp);
        });

        this.$nextTick(() => {
          this.isSearchLoading = false;

          // Fetch count for each analytics type (views, counts, bounces);
          Object.keys(this.charts).forEach((k) => {
            this.charts[k].data = null;
            this.charts[k].donutData = null;

            // Fetch views, clicks, bounces for every campaign.
            this.getData(k, this.form.campaigns);
          });
        });
      });
    }
  },
});
</script>
