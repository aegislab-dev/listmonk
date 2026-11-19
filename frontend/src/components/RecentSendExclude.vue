<template>
  <section v-if="campaignType !== 'optin'" class="recent-exclude" data-cy="recent-exclude">
    <b-switch :value="value.enabled" :disabled="disabled" data-cy="recent-enabled" @input="change({ enabled: $event })">
      {{ $t('campaigns.recent.title') }}
    </b-switch>

    <template v-if="value.enabled">
      <div class="recent-inputs">
        <b-field :label="$t('campaigns.recent.period')">
          <b-select :value="value.days" :disabled="disabled" expanded data-cy="recent-days" @input="change({ days: $event })">
            <option v-for="days in daysOptions" :key="days" :value="days">
              {{ $t('campaigns.recent.days', { days }) }}
            </option>
          </b-select>
        </b-field>
        <b-field :label="$t('campaigns.recent.scope')">
          <b-select :value="value.scope" :disabled="disabled" expanded data-cy="recent-scope" @input="change({ scope: $event })">
            <option v-for="scope in scopes" :key="scope" :value="scope">
              {{ $t(`campaigns.recent.scope.${scope}`) }}
            </option>
          </b-select>
        </b-field>
      </div>
      <p class="recent-help">{{ $t(`campaigns.recent.scopeHelp.${value.scope}`) }}</p>

      <div v-if="value.scope === 'campaigns'" class="recent-campaigns">
        <b-taglist v-if="value.campaignIds.length">
          <b-tag v-for="id in value.campaignIds" :key="id" :closable="!disabled" @close="removeCampaign(id)">
            {{ campaignNames[id] || `#${id}` }}
          </b-tag>
        </b-taglist>
        <b-field :label="$t('campaigns.recent.previousCampaigns')"
          :message="$t('campaigns.recent.maxCampaigns', { max: maxCampaigns })">
          <b-autocomplete v-model="query" :data="campaignOptions" field="name" clear-on-select keep-first open-on-focus
            :placeholder="$t('campaigns.recent.searchCampaigns')" data-cy="recent-campaign-search"
            :disabled="disabled || value.campaignIds.length >= maxCampaigns"
            @focus="searchCampaigns" @select="addCampaign">
            <template #empty>{{ $t('campaigns.recent.noCampaigns') }}</template>
          </b-autocomplete>
        </b-field>
      </div>

      <div aria-live="polite">
        <p v-if="pending" class="recent-help" role="status">{{ $t('campaigns.recent.calculating') }}</p>
        <p v-else-if="error" class="recent-error" role="alert" data-cy="recent-error">{{ error }}</p>
        <template v-else-if="counts">
          <table class="recent-table" :aria-label="$t('campaigns.recent.preview')" data-cy="recent-counts">
            <tbody>
              <tr><th scope="row">{{ $t('campaigns.recent.target') }}</th><td>{{ people(counts.target) }}</td></tr>
              <tr>
                <th scope="row">{{ $t('campaigns.recent.excluded', { days: value.days }) }}</th>
                <td>−{{ people(counts.excluded) }}</td>
              </tr>
              <tr><th scope="row">{{ $t('campaigns.recent.willSend') }}</th><td>{{ people(counts.willSend) }}</td></tr>
            </tbody>
          </table>
          <p class="recent-help" data-cy="recent-as-of">{{ $t('campaigns.recent.asOf', { date: formatDate(counts.asOf) }) }}</p>
          <p class="recent-help">{{ $t('campaigns.recent.bounces') }}</p>
          <p v-if="coverageWarning" class="recent-warning" data-cy="recent-coverage">{{ coverageWarning }}</p>
        </template>
        <p v-else-if="!listIds.length" class="recent-help">{{ $t('campaigns.recent.selectLists') }}</p>
      </div>
    </template>

    <p class="recent-notice" data-cy="recent-notice">{{ $t('campaigns.recent.notice') }}</p>
  </section>
</template>

<script>
import dayjs from 'dayjs';
import {
  RECENT_DAYS, RECENT_MAX_CAMPAIGNS, RECENT_SCOPES, recentRulePayload,
} from '../recent-send-exclude';

export default {
  props: {
    campaignId: { type: Number, default: 0 },
    listIds: { type: Array, required: true },
    campaignType: { type: String, default: 'regular' },
    disabled: Boolean,
    value: { type: Object, required: true },
  },

  data() {
    return {
      daysOptions: RECENT_DAYS,
      scopes: RECENT_SCOPES,
      maxCampaigns: RECENT_MAX_CAMPAIGNS,
      counts: null,
      pending: false,
      error: '',
      previewTimer: null,
      previewController: null,
      query: '',
      options: [],
      searchTimer: null,
      searchController: null,
      campaignNames: {},
    };
  },

  computed: {
    previewKey() {
      if (!this.$can('campaigns:manage_all', 'campaigns:manage') || this.campaignType === 'optin'
        || !this.value.enabled || !this.listIds.length
        || (this.value.scope === 'campaigns' && !this.value.campaignIds.length)) return '';

      return JSON.stringify({ ...recentRulePayload(this.value), list_ids: this.listIds, campaign_id: this.campaignId });
    },

    coverageWarning() {
      if (!this.counts.recordsFrom) return this.$t('campaigns.recent.noRecords');
      if (dayjs(this.counts.recordsFrom).isAfter(dayjs(this.counts.asOf).subtract(this.value.days, 'day'))) {
        return this.$t('campaigns.recent.coverage', { date: this.formatDate(this.counts.recordsFrom, 'YYYY-MM-DD') });
      }
      return '';
    },

    campaignOptions() {
      return this.options.filter(({ id, type }) => type === 'regular' && id !== this.campaignId
        && !this.value.campaignIds.includes(id));
    },

    selectedCampaignIds() {
      return this.value.enabled && this.value.scope === 'campaigns' ? this.value.campaignIds.join(',') : '';
    },
  },

  watch: {
    previewKey: { immediate: true, handler: 'schedulePreview' },
    query: 'searchCampaigns',
    selectedCampaignIds: { immediate: true, handler: 'loadSelectedNames' },
  },

  beforeDestroy() {
    clearTimeout(this.previewTimer);
    clearTimeout(this.searchTimer);
    this.previewController?.abort();
    this.searchController?.abort();
  },

  methods: {
    change(patch) {
      this.$emit('input', { ...this.value, ...patch });
    },

    formatDate(value, format = 'YYYY-MM-DD HH:mm') {
      return dayjs(value).format(format);
    },

    people(count) {
      return this.$t('campaigns.recent.people', { count: count.toLocaleString(this.$i18n.locale) });
    },

    schedulePreview(key) {
      clearTimeout(this.previewTimer);
      this.previewController?.abort();
      this.counts = null;
      this.error = '';
      this.pending = Boolean(key);
      if (!key) return;

      const controller = new AbortController();
      this.previewController = controller;
      this.previewTimer = setTimeout(async () => {
        try {
          const counts = await this.$api.previewRecentExclude(JSON.parse(key), controller.signal);
          if (!controller.signal.aborted && this.previewKey === key) this.counts = counts;
        } catch (err) {
          if (!controller.signal.aborted && this.previewKey === key) {
            this.error = err.response?.data?.message || this.$t('campaigns.recent.error');
          }
        } finally {
          if (!controller.signal.aborted && this.previewKey === key) this.pending = false;
        }
      }, 500);
    },

    searchCampaigns() {
      clearTimeout(this.searchTimer);
      this.searchController?.abort();
      this.options = [];
      const controller = new AbortController();
      this.searchController = controller;
      this.searchTimer = setTimeout(async () => {
        try {
          const { results } = await this.$api.searchRecentCampaigns(this.query, controller.signal);
          if (!controller.signal.aborted) this.options = results;
        } catch (err) {
          if (!controller.signal.aborted) this.options = [];
        }
      }, 250);
    },

    loadSelectedNames() {
      this.value.campaignIds.filter((id) => !(id in this.campaignNames)).forEach((id) => {
        this.$set(this.campaignNames, id, '');
        this.$api.getRecentCampaignOption(id).then(({ name }) => {
          this.$set(this.campaignNames, id, name);
        }).catch(() => {});
      });
    },

    addCampaign(campaign) {
      if (!campaign || this.disabled || this.value.campaignIds.length >= this.maxCampaigns
        || this.value.campaignIds.includes(campaign.id)) return;
      this.$set(this.campaignNames, campaign.id, campaign.name);
      this.change({ campaignIds: [...this.value.campaignIds, campaign.id] });
    },

    removeCampaign(id) {
      this.change({ campaignIds: this.value.campaignIds.filter((selected) => selected !== id) });
    },
  },
};
</script>

<style scoped>
.recent-exclude { padding: 15px 16px; border: 1px solid #dbdbdb; border-radius: 6px; margin-bottom: 20px; }
.recent-exclude > .switch { font-weight: 600; }
.recent-inputs { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 2fr); gap: 12px; margin-top: 12px; }
.recent-inputs .field { margin-bottom: 6px; }
.recent-campaigns { margin: 10px 0; }
.recent-campaigns .tags { margin-bottom: 4px; overflow-wrap: anywhere; }
.recent-help { color: #616161; font-size: .8125rem; }
.recent-table { width: 100%; margin: 10px 0 5px; font-size: .875rem; }
.recent-table th, .recent-table td { padding: 7px 10px; border-bottom: 1px solid #e9e9e9; }
.recent-table th { font-weight: 400; }
.recent-table td { text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
.recent-table tr:last-child { background: #f1f3fb; color: #24378a; }
.recent-table tr:last-child th, .recent-table tr:last-child td { font-weight: 700; border: 0; }
.recent-notice, .recent-warning, .recent-error { border-radius: 4px; padding: 8px 10px; margin-top: 9px; font-size: .8125rem; }
.recent-notice { background: #eef2fc; color: #263675; }
.recent-warning { background: #fff5db; color: #614600; }
.recent-error { background: #fff0f1; color: #852330; }
@media (max-width: 440px) {
  .recent-inputs { grid-template-columns: 1fr; gap: 2px; }
}
</style>
