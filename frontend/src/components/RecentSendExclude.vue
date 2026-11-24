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
              <tr>
                <th scope="row">
                  <template v-if="started">{{ $t('campaigns.recent.sentTarget') }}</template>
                  <template v-else>
                    {{ $t('campaigns.recent.willSend') }}
                    <span class="recent-same" data-cy="recent-same-basis">· {{ $t('campaigns.recent.willSendSame') }}</span>
                  </template>
                </th>
                <td>{{ people(counts.willSend) }}</td>
              </tr>
            </tbody>
          </table>
          <p class="recent-help" data-cy="recent-as-of">{{ $t('campaigns.recent.asOf', { date: formatDate(counts.asOf) }) }}</p>
          <p class="recent-help">{{ $t('campaigns.recent.bounces') }}</p>
          <p v-if="coverageWarning" class="recent-warning" data-cy="recent-coverage">{{ coverageWarning }}</p>
        </template>
        <p v-else-if="!listIds.length" class="recent-help">{{ $t('campaigns.recent.selectLists') }}</p>
      </div>
    </template>

    <!-- LST-65: a campaign that has started says how many people the rule left out of its send. -->
    <div v-if="started && value.enabled" class="recent-made" role="status" data-cy="recent-held">
      ✓ {{ $t('campaigns.recent.held', { count: (value.held || 0).toLocaleString($i18n.locale), days: value.days, scope: $t(`campaigns.recent.scope.${value.scope}`) }) }}
    </div>

    <p class="recent-notice" data-cy="recent-notice">{{ $t(started && disabled ? 'campaigns.recent.locked' : 'campaigns.recent.notice') }}</p>

    <!-- The list of the people the rule leaves out is a side action: the rule works without it
         (LST-46 makes the list as a separate, optional step). -->
    <div v-if="showMadeLine || (madeList && madeList.error && madeListInUse) || canMakeList" class="recent-optional">
      <div v-if="showMadeLine" class="recent-made" role="status" data-cy="recent-list-made">
        ✓ {{ $t(madeList.saved ? 'campaigns.recent.listMade' : 'campaigns.recent.listMadeNew') }}
        <router-link :to="{ name: 'list', params: { id: madeList.id } }">{{ $t('lists.draw.viewList') }}</router-link>
      </div>
      <p v-else-if="madeList && madeList.error && madeListInUse" class="recent-error" role="alert" data-cy="recent-list-save-error">
        {{ madeList.error }}
      </p>
      <div v-else-if="canMakeList" class="recent-make">
        <b-button size="is-small" type="is-primary" outlined class="recent-make-button" data-cy="recent-make-list" @click="openList">
          {{ $t('campaigns.recent.makeList') }}
        </b-button>
      </div>
    </div>

    <b-modal v-if="listOpen" :active="true" has-modal-card scroll="keep" :aria-modal="true" v-bind="{ ariaRole: 'dialog' }"
      aria-labelledby="recent-list-title" custom-class="recent-list-modal" :width="560"
      :can-cancel="creating ? [] : ['escape', 'outside']" @close="closeList">
      <form @submit.prevent="createList">
        <div class="modal-card content recent-list-card" data-cy="recent-list-modal">
          <header class="modal-card-head"><h4 id="recent-list-title">{{ $t('campaigns.recent.listTitle') }}</h4></header>
          <section class="modal-card-body">
            <b-field v-if="isAdmin" :label="$t('teams.drawTeam')">
              <b-select v-model="team" expanded :placeholder="$t('teams.choose')" data-cy="recent-list-team"
                :loading="loadingTeams" :disabled="creating">
                <option v-if="sourceTeams.length" :value="SAME_TEAMS">
                  {{ $t('campaigns.recent.sameTeams', { name: sourceTeamNames }) }}
                </option>
                <option v-for="item in teamChoices" :key="item.tag" :value="item.tag">{{ item.name }}</option>
                <option v-if="value.scope !== 'team'" :value="NO_TEAM">{{ $t('teams.noTeamChoice') }}</option>
              </b-select>
            </b-field>
            <div v-else class="field">
              <p class="label">{{ $t('teams.drawTeam') }}</p>
              <b-tag type="is-info is-light" data-cy="recent-list-team-fixed">{{ teamName($teamTag()) }}</b-tag>
            </div>
            <b-field :label="$t('campaigns.recent.listName')">
              <b-input v-model="listName" :maxlength="200" :has-counter="false" required :disabled="creating"
                data-cy="recent-list-name" />
            </b-field>
            <p class="recent-note" data-cy="recent-list-note">
              {{ $t(savedCampaign ? 'campaigns.recent.listNote' : 'campaigns.recent.listNoteNew',
                    { added: counts.willSend.toLocaleString($i18n.locale), excluded: counts.excluded.toLocaleString($i18n.locale) }) }}
            </p>
            <p v-if="listError" class="recent-error" role="alert" data-cy="recent-list-error">{{ listError }}</p>
          </section>
          <footer class="modal-card-foot">
            <b-button :disabled="creating" @click="closeList">{{ $t('globals.buttons.close') }}</b-button>
            <b-button native-type="submit" type="is-primary" :disabled="!canCreate" :loading="creating"
              data-cy="recent-list-create">
              {{ $t('campaigns.recent.listCreate') }}
            </b-button>
          </footer>
        </div>
      </form>
    </b-modal>
  </section>
</template>

<script>
import dayjs from 'dayjs';
import {
  RECENT_DAYS, RECENT_MAX_CAMPAIGNS, RECENT_SCOPES, recentRulePayload,
} from '../recent-send-exclude';
import { NO_TEAM, teamName } from '../teams';

// The team choice that follows the source lists' teams (sent as no team field).
const SAME_TEAMS = '__same__';
// The longest list name the server takes.
const LIST_NAME_MAX = 200;

export default {
  props: {
    campaignId: { type: Number, default: 0 },
    listIds: { type: Array, required: true },
    campaignType: { type: String, default: 'regular' },
    disabled: Boolean,
    value: { type: Object, required: true },
    // The campaign has started (running, paused, finished or cancelled): the page says how many
    // people the rule left out of its send (LST-65).
    started: Boolean,
    // The campaign's lists can still be changed (a new, draft or scheduled campaign).
    listsEditable: Boolean,
    // The campaign exists on the server, so making the list also saves it.
    savedCampaign: Boolean,
    // The list made from this rule and whether the campaign was saved with it (kept by the page).
    madeList: { type: Object, default: null },
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
      NO_TEAM,
      SAME_TEAMS,
      listOpen: false,
      listName: '',
      team: null,
      teams: [],
      loadingTeams: false,
      creating: false,
      listError: '',
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

    isAdmin() { return this.$can('lists:manage_all'); },

    // The box is the place to make the list only when its numbers are on screen and there is
    // somebody left to send to.
    canMakeList() {
      return this.listsEditable && !this.disabled && this.$canCreateList() && !this.pending && !this.error
        && !!this.counts && this.counts.willSend > 0;
    },

    madeListInUse() {
      return !!this.madeList && this.listIds.length === 1 && this.listIds[0] === this.madeList.id;
    },

    // The result line stays while the preview shows no new overlap; once somebody would be
    // excluded again the button comes back (a new result can be made).
    showMadeLine() {
      return this.madeListInUse && !this.madeList.error && (!this.savedCampaign || this.madeList.saved)
        && (!this.counts || this.counts.excluded === 0);
    },

    sourceTeams() {
      const available = new Set(this.teams.map((item) => item.tag));
      const source = this.counts?.teamTags || [];
      return source.length && source.every((tag) => available.has(tag)) ? source : [];
    },

    sourceTeamNames() { return this.sourceTeams.map(teamName).join(', '); },

    // A single source team is already the default choice: do not list it twice.
    teamChoices() {
      return this.sourceTeams.length === 1 ? this.teams.filter((item) => item.tag !== this.sourceTeams[0]) : this.teams;
    },

    defaultListName() {
      const names = this.listIds.map((id) => (this.$store.state.lists.results || []).find((l) => l.id === id)?.name)
        .filter(Boolean);
      if (!names.length) return '';
      const base = names.length > 1
        ? this.$t('lists.recentExclude.moreLists', { name: names[0], count: names.length - 1 }) : names[0];
      const suffix = this.$t('lists.recentExclude.nameSuffix', { days: this.value.days, date: dayjs().format('YYYY-MM-DD') });
      // A list name takes at most 200 characters (counted like the server, by code point): a long
      // source name is cut so that the suffix fits, as the server does for its default name.
      const room = Math.max(0, LIST_NAME_MAX - Array.from(suffix).length);
      return Array.from(base).slice(0, room).join('') + suffix;
    },

    canCreate() {
      return !this.creating && !!this.listName.trim() && (!this.isAdmin || (!this.loadingTeams && this.team !== null));
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
    teamName,

    async openList() {
      this.listOpen = true;
      this.listError = '';
      this.listName = this.defaultListName;
      this.team = null;
      if (!this.isAdmin) return;
      this.loadingTeams = true;
      try {
        const data = await this.$api.getListTeams();
        this.teams = (data.teams || []).filter((team) => team.hasRole);
      } catch (err) {
        this.listError = err.response?.data?.message || this.$t('campaigns.recent.error');
      } finally {
        this.loadingTeams = false;
        // Follow the source lists' teams unless that is impossible, or the person already chose
        // (the choice without a team can be picked while the teams load).
        if (this.team === null || (this.team !== NO_TEAM && !this.teams.some((t) => t.tag === this.team))) {
          this.team = this.sourceTeams.length ? SAME_TEAMS : null;
        }
      }
    },

    closeList() {
      if (!this.creating) this.listOpen = false;
    },

    async createList() {
      if (!this.canCreate || !this.previewKey) return;
      this.creating = true;
      this.listError = '';
      const body = { ...JSON.parse(this.previewKey), name: this.listName.trim() };
      if (this.isAdmin && this.team === NO_TEAM) body.no_team = true;
      else if (this.isAdmin && this.team && this.team !== SAME_TEAMS) body.team = this.team;
      try {
        const result = await this.$api.makeRecentExcludeList(body);
        this.listOpen = false;
        this.$emit('list-made', result);
      } catch (err) {
        this.listError = err.response?.data?.message || this.$t('campaigns.recent.error');
      } finally {
        this.creating = false;
      }
    },

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
.recent-optional { margin-top: 16px; padding-top: 12px; border-top: 1px solid #e3e3e3; }
.recent-optional .recent-made { margin-top: 0; }
.recent-make { margin-top: 0; }
.recent-same { font-weight: 400; font-size: .75rem; color: #616161; margin-left: 4px; }
@media (max-width: 440px) {
  .recent-make-button { width: 100%; }
}
.recent-made { border: 1px solid #bfe0cd; border-radius: 4px; padding: 9px 12px; margin-top: 12px; font-size: .8125rem; background: #e8f5ed; color: #1f4d36; }
.recent-made a { margin-left: 6px; font-weight: 600; }
.recent-note { border-radius: 4px; padding: 8px 10px; margin-top: 10px; font-size: .8125rem; background: #eef2fc; color: #263675; }
.recent-list-card { width: 100%; margin: 0; max-height: calc(100dvh - 32px); }
.recent-list-card .modal-card-head, .recent-list-card .modal-card-body { padding: 16px 20px; }
.recent-list-card .modal-card-foot { background: #f7f7f7; border-top: 1px solid #e9ecf1; padding: 12px 20px; flex-wrap: wrap; gap: 8px; }
.recent-list-card .modal-card-foot button { margin: 0; flex-grow: 0; }
.recent-list-card h4 { margin: 0; }
.recent-warning { background: #fff5db; color: #614600; }
.recent-error { background: #fff0f1; color: #852330; }
@media (max-width: 440px) {
  .recent-inputs { grid-template-columns: 1fr; gap: 2px; }
  .recent-list-card .modal-card-head, .recent-list-card .modal-card-body, .recent-list-card .modal-card-foot { padding: 14px; }
  .recent-list-card .modal-card-foot .is-primary { flex-grow: 1; }
}
</style>
<style>
.recent-list-modal .animation-content { width: calc(100vw - 24px); max-width: 560px; }
</style>
