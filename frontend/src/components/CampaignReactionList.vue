<template>
  <b-modal :active="true" has-modal-card scroll="keep" :aria-modal="true" v-bind="{ ariaRole: 'dialog' }"
    aria-labelledby="reaction-title" custom-class="campaign-reaction-modal" :width="680"
    :can-cancel="creating ? [] : ['escape', 'outside']" @close="$emit('close')">
    <form @submit.prevent="createList">
      <div class="modal-card content reaction-card" data-cy="reaction-list">
        <header class="modal-card-head">
          <h4 id="reaction-title">{{ result && result.followup ? f('doneBoth') : t(result ? 'done' : 'title') }}</h4>
          <template v-if="metadata">
            <p class="reaction-source">
              {{ metadata.campaign.name }} · #{{ metadata.campaign.id }} ·
              {{ $t(`campaigns.status.${metadata.campaign.status}`) }}
              <template v-if="metadata.campaign.startedAt">
                · {{ t('sentDate', { date: date(metadata.campaign.startedAt, 'YYYY-MM-DD') }) }}
              </template>
            </p>
            <div class="reaction-metrics">
              <strong>{{ t('receivedCount', { count: num(tracking.sends) }) }}</strong>
              <b-tag v-if="noSends" type="is-warning is-light" data-cy="reaction-no-sends">
                {{ t('noSendsBadge') }}
              </b-tag>
              <template v-else>
                <b-tag v-if="noOpen" type="is-warning is-light" data-cy="reaction-no-open">
                  {{ t('noOpenBadge') }}
                </b-tag>
                <b-tag v-if="noClick" type="is-warning is-light" data-cy="reaction-no-click">
                  {{ t('noClickBadge') }}
                </b-tag>
                <b-tag v-if="!noOpen && !noClick" type="is-success is-light">{{ t('trackingOK') }}</b-tag>
              </template>
              <b-tag v-if="sending" type="is-warning is-light">{{ t('sendingBadge') }}</b-tag>
            </div>
          </template>
        </header>

        <section v-if="!result" class="modal-card-body">
          <p v-if="loading" role="status">{{ t('loading') }}</p>
          <template v-if="metadata">
            <p v-if="noSends" class="reaction-note is-warning">{{ t('noSends') }}</p>
            <p v-if="!tracking.individualTracking" class="reaction-note is-warning">{{ t('individualOff') }}</p>
            <p v-if="sending" class="reaction-note is-warning">{{ t('sending') }}</p>

            <div v-for="axis in axes" :key="axis.key">
              <div class="reaction-condition" role="group" :aria-label="t(axis.key)">
                <span class="label">{{ t(axis.key) }}</span>
                <b-field>
                  <b-radio-button v-for="choice in choices" :key="choice" v-model="settings[axis.key]"
                    :native-value="choice" :name="axis.key" :disabled="creating || (axis.locked && choice !== 'any')"
                    :data-cy="`reaction-${axis.key}-${choice}`"
                    :title="axis.locked && choice !== 'any' ? axis.help : null" size="is-small">
                    {{ t(`${axis.key}.${choice}`) }}
                  </b-radio-button>
                </b-field>
                <p v-if="axis.locked" class="help">{{ axis.help }}</p>
              </div>
              <p v-if="axis.key === 'open'" class="reaction-note">{{ t('openHelp') }}</p>
            </div>
            <p class="help">{{ t('alwaysExcluded') }}</p>

            <div class="reaction-metrics reaction-check">
              <b-button :loading="checking" :disabled="noSends || creating" data-cy="reaction-check" @click="check">
                {{ t('check') }}
              </b-button>
              <span class="help">{{ t('checkHelp') }}</span>
            </div>

            <template v-if="preview">
              <div class="reaction-results" aria-live="polite" data-cy="reaction-preview">
                <div class="reaction-box">
                  <h5>{{ t('check') }}</h5>
                  <table>
                    <tbody>
                      <tr v-for="row in countRows" :key="row.key" :class="row.style">
                        <td>{{ t(row.key) }}</td>
                        <td :data-cy="`reaction-count-${row.key}`">{{ num(row.count) }}</td>
                      </tr>
                    </tbody>
                  </table>
                </div>
                <div class="reaction-box">
                  <h5>{{ t('sample') }}<template v-if="preview.sample.length"> · {{ t('people', { count: num(preview.sample.length) }) }}</template></h5>
                  <table v-if="preview.sample.length" class="reaction-sample" data-cy="reaction-sample">
                    <thead>
                      <tr><th>{{ $t('globals.fields.name') }}</th><th>{{ $t('subscribers.email') }}</th></tr>
                    </thead>
                    <tbody>
                      <tr v-for="(sample, index) in preview.sample" :key="index">
                        <td>{{ sample.name }}</td><td>{{ sample.email }}</td>
                      </tr>
                    </tbody>
                  </table>
                  <p v-else class="help" data-cy="reaction-empty">{{ t('empty') }}</p>
                </div>
              </div>
              <p class="help">{{ t('checkedAt', { time: date(preview.checkedAt) }) }} {{ t('recheck') }}</p>

              <template v-if="eligible > 0">
                <b-field v-if="isAdmin" :label="$t('teams.drawTeam')">
                  <b-select v-model="team" expanded :placeholder="$t('teams.choose')" data-cy="reaction-team"
                    :loading="loadingTeams" :disabled="creating">
                    <option v-if="sourceTeams.length" value="">{{ t('sameAsSource', { name: sourceTeamNames }) }}</option>
                    <option v-for="item in teams" :key="item.tag" :value="item.tag">{{ item.name }}</option>
                    <option :value="NO_TEAM">{{ $t('teams.noTeamChoice') }}</option>
                  </b-select>
                </b-field>
                <div v-else class="field">
                  <p class="label">{{ $t('teams.drawTeam') }}</p>
                  <b-tag type="is-info is-light" data-cy="reaction-team-fixed">{{ teamName($teamTag()) }}</b-tag>
                  <p class="help">{{ $t('teams.drawMember') }}</p>
                </div>
                <b-field :label="$t('lists.draw.name')">
                  <b-input v-model="name" :maxlength="200" required :disabled="creating"
                    data-cy="reaction-name" @input="nameTouched = true" />
                </b-field>

                <div v-if="offer && offer.available" class="followup-box" data-cy="followup-box">
                  <b-checkbox v-model="followup" :disabled="creating" data-cy="followup-toggle">
                    <strong>{{ f('toggle') }}</strong>
                    <span class="followup-hint">{{ f('toggleHint') }}</span>
                  </b-checkbox>
                  <template v-if="followup">
                    <p v-if="followupSoon" class="reaction-note is-warning" data-cy="followup-warn">
                      {{ f('warn', { days: offer.warnDays }) }}
                    </p>
                    <b-field :label="f('name')">
                      <b-input v-model="followupName" :maxlength="200" :disabled="creating"
                        data-cy="followup-name" @input="followupNameTouched = true" />
                    </b-field>
                    <p class="reaction-note" data-cy="followup-note">
                      <strong>{{ f('recheckTitle') }}</strong> {{ f('recheckBody') }}
                    </p>
                  </template>
                </div>
              </template>

              <!-- Shown whatever this check found, even for nobody: the follow-up already made. -->
              <div v-if="offer && offer.existing" class="followup-box" data-cy="followup-box">
                <p>
                  <strong>{{ f('existing') }}</strong>
                  <a href="#" data-cy="followup-open-existing" @click.prevent="openFollowup(offer.existing.id)">
                    {{ f('open', { id: offer.existing.id }) }}
                  </a>
                  · {{ $t(`campaigns.status.${offer.existing.status}`) }}
                  <template v-if="existingCondition"> · {{ f('condition') }} {{ existingCondition }}</template>
                  <template v-if="offer.existing.condition"> · {{ f('listCount', { count: num(offer.existing.count || 0) }) }}</template>
                </p>
                <p class="help">{{ f('existsHint') }}</p>
              </div>
            </template>
          </template>
          <p v-if="error" class="reaction-note is-warning" role="alert" data-cy="reaction-error">{{ error }}</p>
        </section>

        <section v-else class="modal-card-body" data-cy="reaction-done">
          <p class="reaction-note is-success" data-cy="reaction-done-count">
            {{ result.followup ? f('addedBoth', { count: num(result.counts.eligible) }) : t('added', { count: num(result.counts.eligible) }) }}
          </p>
          <p class="reaction-done-name"><strong>{{ result.list.name }}</strong></p>
          <div class="reaction-metrics">
            <b-tag type="is-info is-light">{{ $t('lists.types.private') }}</b-tag>
            <b-tag v-for="tag in result.list.tags" :key="tag" type="is-info is-light">{{ teamName(tag) }}</b-tag>
          </div>
          <p v-if="result.followup" class="reaction-done-name" data-cy="followup-done">
            {{ f('draft') }} <strong>{{ result.followup.name }}</strong> · #{{ result.followup.id }}
            · {{ $t(`campaigns.status.${result.followup.status}`) }}
          </p>
          <p class="help">
            {{ t('checkedAt', { time: date(result.checkedAt) }) }}
            <template v-if="result.followup">{{ f('bannerRecheck') }}</template>
          </p>
        </section>

        <footer class="modal-card-foot">
          <b-button :disabled="creating" @click="$emit('close')">{{ $t('globals.buttons.close') }}</b-button>
          <b-button v-if="!result" native-type="submit" type="is-primary" :disabled="!canCreate"
            :loading="creating" data-cy="reaction-create">
            {{ createLabel }}
          </b-button>
          <template v-else>
            <b-button data-cy="reaction-view" @click="viewList">{{ $t('lists.draw.viewList') }}</b-button>
            <b-button v-if="result.followup" type="is-primary" data-cy="followup-view"
              @click="openFollowup(result.followup.id)">
              {{ f('openDraft') }}
            </b-button>
            <b-button v-else-if="$can('campaigns:manage', 'campaigns:manage_all')" type="is-primary"
              data-cy="reaction-campaign" @click="makeCampaign">
              {{ $t('lists.draw.makeCampaign') }}
            </b-button>
          </template>
        </footer>
      </div>
    </form>
  </b-modal>
</template>

<script>
import Vue from 'vue';
import dayjs from 'dayjs';
import { NO_TEAM, teamName } from '../teams';
import { conditionWords } from '../followup';

export default Vue.extend({
  name: 'CampaignReactionList',
  props: {
    campaignId: { type: Number, required: true },
  },
  data() {
    return {
      NO_TEAM,
      choices: ['any', 'yes', 'no'],
      settings: { open: 'any', click: 'any' },
      metadata: null,
      preview: null,
      result: null,
      error: '',
      teams: [],
      team: null,
      name: '',
      nameTouched: false,
      loading: true,
      loadingTeams: false,
      checking: false,
      creating: false,
      followup: false,
      followupName: '',
      followupNameTouched: false,
    };
  },
  computed: {
    isAdmin() { return this.$can('lists:manage_all'); },
    tracking() { return this.metadata.tracking; },
    noSends() { return this.tracking.sends === 0; },
    noOpen() { return !this.tracking.individualTracking || this.tracking.viewers === 0; },
    noClick() { return !this.tracking.individualTracking || this.tracking.clickers === 0; },
    sending() { return ['running', 'paused'].includes(this.metadata.campaign.status); },
    axes() {
      return [
        { key: 'open', locked: this.noSends || this.noOpen, help: this.t(this.noSends ? 'noSendsHelp' : 'noOpenHelp') },
        { key: 'click', locked: this.noSends || this.noClick, help: this.t(this.noSends ? 'noSendsHelp' : 'noClickHelp') },
      ];
    },
    eligible() { return this.preview?.counts.eligible || 0; },
    countRows() {
      const n = this.preview.counts;
      return [
        { key: 'received', count: this.preview.tracking.sends },
        { key: 'matched', count: n.matched },
        { key: 'blocked', count: n.blocklisted, style: 'deduction' },
        { key: 'campaignOptOut', count: n.campaignOptOut, style: 'deduction' },
        { key: 'allOptOut', count: n.allOptOut, style: 'deduction' },
        { key: 'outOfScope', count: n.outOfScope, style: 'deduction' },
        { key: 'eligible', count: n.eligible, style: 'total' },
      ];
    },
    sourceTeams() {
      const available = new Set(this.teams.map((item) => item.tag));
      const source = this.metadata?.sourceTeams || [];
      return source.every((tag) => available.has(tag)) ? source : [];
    },
    sourceTeamNames() { return this.sourceTeams.map(teamName).join(', '); },
    canCreate() {
      return this.eligible > 0 && !!this.name.trim() && !this.checking && !this.creating
        && (!this.isAdmin || (!this.loadingTeams && this.team !== null))
        && (!this.wantsFollowup || !!this.followupName.trim());
    },
    // What the server says about making a follow-up draft with the list.
    offer() { return this.metadata?.followup || null; },
    // The condition the existing follow-up was made with, in words.
    existingCondition() {
      return conditionWords(this.offer?.existing?.condition, (key) => this.$t(key));
    },
    wantsFollowup() { return this.followup && !!this.offer?.available; },
    followupSoon() {
      return this.offer && this.offer.daysSince !== null && this.offer.daysSince < this.offer.warnDays;
    },
    createLabel() {
      if (!this.metadata || this.noSends || (this.preview && this.eligible === 0)) return this.t('create');
      if (!this.preview) return this.t('checkFirst');
      return this.wantsFollowup
        ? this.f('createBoth', { count: this.num(this.eligible) })
        : this.t('createCount', { count: this.num(this.eligible) });
    },
    conditionKey() { return `${this.settings.open}/${this.settings.click}`; },
  },
  watch: {
    conditionKey() {
      this.preview = null;
      this.error = '';
    },
    sourceTeams(source) {
      if (this.team === null && source.length) this.team = '';
      if (this.team === '' && !source.length) this.team = null;
    },
  },
  methods: {
    teamName,
    t(key, params) { return this.$t(`campaigns.reactionList.${key}`, params); },
    f(key, params) { return this.$t(`campaigns.followup.${key}`, params); },
    num(value) { return this.$utils.formatNumber(value); },
    date(value, format = 'YYYY-MM-DD HH:mm Z') { return dayjs(value).format(format); },
    showError(e) { this.error = e.response?.data?.message || this.t('error'); },
    async loadTracking() {
      try {
        this.metadata = await this.$api.previewCampaignReactionList(this.campaignId, { tracking_only: true });
      } catch (e) {
        this.showError(e);
      } finally {
        this.loading = false;
      }
    },
    async loadTeams() {
      this.loadingTeams = true;
      try {
        const data = await this.$api.getListTeams();
        this.teams = (data.teams || []).filter((team) => team.hasRole);
      } catch (e) {
        this.showError(e);
      } finally {
        this.loadingTeams = false;
      }
    },
    async check() {
      if (this.checking || this.noSends) return;
      this.checking = true;
      this.preview = null;
      this.error = '';
      const key = this.conditionKey;
      try {
        const data = await this.$api.previewCampaignReactionList(this.campaignId, { ...this.settings });
        if (key === this.conditionKey) {
          this.metadata = data;
          this.preview = data;
          if (!this.nameTouched) this.name = data.defaultName || '';
          if (!this.followupNameTouched) this.followupName = data.followup?.defaultName || '';
        }
      } catch (e) {
        if (key === this.conditionKey) this.showError(e);
      } finally {
        this.checking = false;
      }
    },
    refreshLists() {
      return this.$api.getLists({ minimal: true, per_page: 'all', status: 'active' });
    },
    async createList() {
      if (!this.canCreate) return;
      this.creating = true;
      this.error = '';
      let teamBody = {};
      if (this.isAdmin && this.team === NO_TEAM) teamBody = { no_team: true };
      else if (this.isAdmin && this.team) teamBody = { team: this.team };
      try {
        // Let the server use the final eligible count for the suggested name.
        const name = this.nameTouched ? this.name.trim() : '';
        const followupBody = this.wantsFollowup
          ? { followup: true, followup_name: this.followupNameTouched ? this.followupName.trim() : '' } : {};
        const result = await this.$api.makeCampaignReactionList(this.campaignId, {
          ...this.settings, ...teamBody, ...followupBody, name,
        });
        // Campaign drafts use the shared list options. A refresh failure leaves the saved list intact.
        await this.refreshLists().catch(() => {});
        this.result = result;
        this.metadata = { ...this.metadata, campaign: result.campaign, tracking: result.tracking };
        this.$refreshProfile().catch(() => {});
      } catch (e) {
        this.showError(e);
        // Someone else made the follow-up first: show it instead of the checkbox.
        if (e.response?.status === 409) {
          this.followup = false;
          this.loadTracking();
        }
      } finally {
        this.creating = false;
      }
    },
    openFollowup(id) {
      this.$router.push({ name: 'campaign', params: { id } });
      this.$emit('close');
    },
    viewList() {
      this.$router.push({ name: 'list', params: { id: this.result.list.id } });
      this.$emit('close');
    },
    async makeCampaign() {
      const { id } = this.result.list;
      const isAvailable = () => this.$store.state.lists.results?.some((list) => list.id === id);
      if (!isAvailable()) {
        try {
          await this.refreshLists();
        } catch {
          return;
        }
        if (!isAvailable()) {
          this.$utils.toast(this.$t('globals.messages.notFound', { name: this.$tc('globals.terms.list', 1) }), 'is-danger');
          return;
        }
      }
      this.$router.push(`/campaigns/new?list_id=${id}`);
      this.$emit('close');
    },
  },
  mounted() {
    this.loadTracking();
    if (this.isAdmin) this.loadTeams();
  },
});
</script>

<style>
.campaign-reaction-modal .animation-content { width: calc(100vw - 24px); }
</style>
<style scoped>
.reaction-card { width: 100%; margin: 0; max-height: calc(100dvh - 32px); }
.reaction-card .modal-card-head, .reaction-card .modal-card-body { padding: 16px 20px; }
.reaction-card .modal-card-foot { background: #f7f7f7; border-top: 1px solid #e9ecf1; padding: 12px 20px; flex-wrap: wrap; gap: 8px; }
.reaction-card .modal-card-foot button { margin: 0; flex-grow: 0; }
.reaction-card h4 { margin: 0; }
.reaction-source { font-size: .85rem; color: #535b65; margin: 5px 0 8px !important; }
.reaction-metrics { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.reaction-metrics .tag.is-success { background: #e8f5ed; color: #225d40; }
.reaction-metrics .tag.is-warning { background: #fff1d6; color: #805410; }
.reaction-condition { display: grid; grid-template-columns: 64px 1fr; align-items: baseline; gap: 5px 8px; margin-bottom: 8px; }
.reaction-condition .field, .reaction-condition .label { margin-bottom: 0; }
.reaction-condition ::v-deep .radio.button.is-selected {
  background: #eaf2ff;
  border-color: #2160c4;
  color: #174d9e;
  font-weight: 700;
}
.reaction-condition .help { grid-column: 2; margin: 0; }
.reaction-note { margin: 8px 0; color: #345276; padding: 8px 10px; background: #eef4fc; border-left: 3px solid #2364c6; font-size: .85rem; }
.reaction-note.is-warning { background: #fff7e5; color: #724908; border-color: #da9c1c; }
.reaction-note.is-success { background: #eaf6ef; color: #215b3c; border-color: #36a773; }
.reaction-check { margin: 12px 0; }
.reaction-results { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; margin-bottom: 10px; }
.reaction-box { background: #f7f9fa; border: 1px solid #dce3e9; border-radius: 4px; padding: 9px 12px; min-width: 0; }
.reaction-box h5 { font-size: .9rem; margin-bottom: 5px; }
.reaction-box table { font-size: .8rem; }
.reaction-box td, .reaction-box th { padding: 4px 1px; border-color: #e5e7eb; }
.followup-box { margin-top: 14px; padding-top: 12px; border-top: 1px solid #e5e7eb; }
.followup-box p { margin: 0 0 4px; }
.followup-box .reaction-note { margin: 8px 0; }
.followup-hint { color: #5b6573; margin-left: 4px; }
.reaction-box td:last-child { text-align: right; font-variant-numeric: tabular-nums; }
.reaction-box .deduction { color: #535b65; }
.reaction-box .total { color: #1451a6; font-weight: 700; }
.reaction-box .reaction-sample td, .reaction-box .reaction-sample th { padding: 1px 0; text-align: left; overflow-wrap: anywhere; }
.reaction-done-name { overflow-wrap: anywhere; }
@media screen and (max-width: 550px) {
  .reaction-card .modal-card-head, .reaction-card .modal-card-body, .reaction-card .modal-card-foot { padding: 14px; }
  .reaction-condition { grid-template-columns: 43px 1fr; gap: 5px; }
  .reaction-results { grid-template-columns: 1fr; gap: 8px; }
  .reaction-card .modal-card-foot .is-primary { flex-grow: 1; }
}
</style>
