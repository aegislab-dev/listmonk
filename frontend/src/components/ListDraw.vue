<template>
  <form @submit.prevent="onSubmit">
    <div class="modal-card content list-draw" style="width: auto" data-cy="list-draw">
      <header class="modal-card-head">
        <b-tag class="is-pulled-right" :class="source.axmFilter ? 'is-info is-light' : ''">
          {{ source.axmFilter ? $t('lists.dynamic.dynamic') : $t('lists.dynamic.static') }}
        </b-tag>
        <h4>{{ done ? $t('lists.draw.done') : $t('lists.draw.title') }}</h4>
        <p class="is-size-7 has-text-grey source-line" data-cy="draw-source">
          {{ $t('lists.draw.source', { name: source.name, num: $utils.formatNumber(source.subscriberCount) }) }}
        </p>
      </header>

      <section v-if="!done" class="modal-card-body">
        <div class="draw-row">
          <b-field :label="$t('lists.draw.count')" :type="valueError ? 'is-danger' : ''" :message="valueError">
            <b-input v-model="form.value" inputmode="numeric" autocomplete="off" maxlength="8" :has-counter="false"
              :placeholder="form.unit === 'percent' ? '10' : '1,000'" ref="focus" data-cy="draw-value"
              :aria-label="$t('lists.draw.count')" />
          </b-field>
          <b-field :label="$t('lists.draw.unit')">
            <b-select v-model="form.unit" data-cy="draw-unit">
              <option value="count">{{ $t('lists.draw.unitCount') }}</option>
              <option value="percent">{{ $t('lists.draw.unitPercent') }}</option>
            </b-select>
          </b-field>
        </div>

        <!-- The team of the new list (LST-24) -->
        <div v-if="showTeam" class="field draw-team" data-cy="draw-team-field">
          <p class="label">{{ $t('teams.drawTeam') }} <span v-if="teamRequired" class="has-text-danger">*</span></p>
          <b-select v-if="isAdmin" v-model="team" expanded :placeholder="$t('teams.choose')" data-cy="draw-team">
            <option v-if="sourceTeams.length" value="">{{ $t('teams.sameAsSource', { name: sourceTeamNames }) }}</option>
            <option v-for="t in adminTeams" :key="t.tag" :value="t.tag">{{ t.name }}</option>
            <option :value="NO_TEAM">{{ $t('teams.noTeamChoice') }}</option>
          </b-select>
          <b-tag v-else type="is-info is-light" data-cy="draw-team-fixed">
            <b-icon icon="account-multiple" size="is-small" /> {{ myTeamName }}
          </b-tag>
          <p class="help" :class="{ 'is-danger': teamRequired && !team }">{{ teamHelp }}</p>
        </div>

        <div class="draw-box">
          <p class="label">{{ $t('lists.draw.excludeTitle') }}</p>
          <div class="draw-row">
            <b-field :label="$t('lists.draw.scope')">
              <b-select v-model="form.scope" data-cy="draw-scope">
                <option value="same_source">{{ $t('lists.draw.scopeSame') }}</option>
                <option value="all">{{ $t('lists.draw.scopeAll') }}</option>
              </b-select>
            </b-field>
            <b-field :label="$t('lists.draw.period')">
              <b-select v-model="form.period" data-cy="draw-period">
                <option value="all">{{ $t('lists.draw.periodAll') }}</option>
                <option value="days">{{ $t('lists.draw.periodDays') }}</option>
              </b-select>
            </b-field>
            <b-field v-if="form.period === 'days'" :label="$t('lists.draw.days')" :type="daysError ? 'is-danger' : ''"
              :message="daysError">
              <b-input v-model="form.days" inputmode="numeric" autocomplete="off" maxlength="4" :has-counter="false"
                class="draw-days" data-cy="draw-days" :aria-label="$t('lists.draw.days')" />
            </b-field>
          </div>
          <p class="help">{{ $t('lists.draw.scopeHelp') }}</p>
          <p class="draw-info" data-cy="draw-ledger-note">{{ $t('lists.draw.ledgerNote') }}</p>
          <p class="draw-info" data-cy="draw-always">{{ $t('lists.draw.alwaysExcluded') }}</p>
        </div>

        <div class="draw-check">
          <b-button :loading="checking" :disabled="!!validation" data-cy="draw-check" @click="check">
            {{ $t('lists.draw.check') }}
          </b-button>
          <span class="has-text-grey is-size-7">{{ $t('lists.draw.checkHelp') }}</span>
        </div>

        <div v-if="counts" class="draw-box draw-counts" data-cy="draw-counts" aria-live="polite">
          <table class="draw-table">
            <tbody>
              <tr><td>{{ $t('lists.draw.rowTarget') }}</td><td data-cy="draw-target">{{ num(counts.target) }}</td></tr>
              <tr class="sub"><td>{{ $t('lists.draw.rowDrawn') }}</td><td data-cy="draw-already-drawn">{{ num(counts.alreadyDrawn) }}</td></tr>
              <tr class="sub"><td>{{ $t('lists.draw.rowReceived') }}</td><td data-cy="draw-already-received">{{ num(counts.alreadyReceived) }}</td></tr>
              <tr><td>{{ $t('lists.draw.rowCandidates') }}</td><td data-cy="draw-candidates">{{ num(counts.candidates) }}</td></tr>
              <tr class="result">
                <td>{{ form.unit === 'percent' ? $t('lists.draw.rowWillPercent', { target: $utils.formatNumber(counts.target), value: form.value }) : $t('lists.draw.rowWill') }}</td>
                <td data-cy="draw-will">{{ num(will) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-if="counts && counts.target > 0 && counts.alreadyReceived === 0" class="draw-note is-warning" data-cy="draw-received-zero">
          {{ $t('lists.draw.receivedZero') }}
        </p>
        <p v-if="counts && will === 0" class="draw-note is-danger" role="alert" data-cy="draw-none">{{ $t('lists.draw.none') }}</p>
        <p v-else-if="counts && counts.requested > counts.candidates" class="draw-note is-warning" role="alert" data-cy="draw-short">
          {{ $t('lists.draw.short', { candidates: $utils.formatNumber(counts.candidates), requested: $utils.formatNumber(counts.requested), will: $utils.formatNumber(will) }) }}
        </p>

        <b-field v-if="counts && will > 0" :label="$t('lists.draw.name')" label-position="on-border"
          :message="$t('lists.draw.nameHelp')">
          <b-input v-model="form.name" :maxlength="200" required data-cy="draw-name" @input="nameTouched = true" />
        </b-field>
        <p v-if="error" class="help is-danger" role="alert" data-cy="draw-error">{{ error }}</p>
      </section>

      <section v-else class="modal-card-body" data-cy="draw-done">
        <p class="draw-note is-success">{{ $t('lists.draw.doneName', { name: result.list.name }) }}</p>
        <div class="draw-box draw-counts">
          <table class="draw-table">
            <tbody>
              <tr><td>{{ $t('lists.draw.rowTarget') }}</td><td>{{ num(result.counts.target) }}</td></tr>
              <tr class="sub"><td>{{ $t('lists.draw.rowDrawn') }}</td><td>{{ num(result.counts.alreadyDrawn) }}</td></tr>
              <tr class="sub"><td>{{ $t('lists.draw.rowReceived') }}</td><td>{{ num(result.counts.alreadyReceived) }}</td></tr>
              <tr><td>{{ $t('lists.draw.rowCandidates') }}</td><td>{{ num(result.counts.candidates) }}</td></tr>
              <tr class="result"><td>{{ $t('lists.draw.rowDone') }}</td><td data-cy="draw-done-count">{{ num(result.counts.drawn) }}</td></tr>
            </tbody>
          </table>
        </div>
        <p class="help">{{ $t('lists.draw.doneHelp', { num: $utils.formatNumber(result.counts.drawn) }) }}</p>
      </section>

      <footer class="modal-card-foot has-text-right">
        <b-button @click="$parent.close()" data-cy="draw-close">{{ $t('globals.buttons.close') }}</b-button>
        <template v-if="!done">
          <b-button native-type="submit" type="is-primary" :loading="creating" :disabled="!canCreate" data-cy="draw-create">
            {{ createLabel }}
          </b-button>
        </template>
        <template v-else>
          <b-button data-cy="draw-view" @click="viewList">{{ $t('lists.draw.viewList') }}</b-button>
          <b-button v-if="$can('campaigns:manage')" type="is-primary" data-cy="draw-campaign" @click="makeCampaign">
            {{ $t('lists.draw.makeCampaign') }}
          </b-button>
        </template>
      </footer>
    </div>
  </form>
</template>

<script>
import Vue from 'vue';
import {
  defaultDrawName, drawBody, localDate, validateDraw, willDraw,
} from '../list-draw';
import { NO_TEAM, teamName, teamTags } from '../teams';

export default Vue.extend({
  name: 'ListDraw',

  props: {
    // The list to draw from, as the lists table has it.
    source: { type: Object, required: true },
    // The teams on the lists the user may read (GET /api/lists/teams).
    teams: { type: Array, default: () => [] },
  },

  data() {
    return {
      form: {
        unit: 'count', value: '', scope: 'same_source', period: 'all', days: '7', name: '',
      },
      NO_TEAM,
      // The team of the new list: a team tag, NO_TEAM, or '' (the source's teams).
      team: '',
      nameTouched: false,
      // The numbers of the last check. Any change of the settings clears them, so what the
      // user sees is what the draw is asked to make.
      counts: null,
      checking: false,
      creating: false,
      error: '',
      touched: false,
      done: false,
      result: null,
    };
  },

  computed: {
    validation() { return validateDraw(this.form); },

    isAdmin() { return this.$can('lists:manage_all'); },
    adminTeams() { return (this.teams || []).filter((t) => t.hasRole); },
    sourceTeams() { return teamTags(this.source.tags); },
    sourceTeamNames() { return this.sourceTeams.map(teamName).join(', '); },
    myTeamName() { return teamName(this.$teamTag()); },
    showTeam() { return this.isAdmin ? this.adminTeams.length > 0 : !!this.$teamTag(); },
    // A source without a team gives the new list nothing to inherit: an admin must choose.
    teamRequired() { return this.isAdmin && this.adminTeams.length > 0 && !this.sourceTeams.length; },
    teamHelp() {
      if (!this.isAdmin) return this.$t('teams.drawMember');
      return this.teamRequired && !this.team ? this.$t('teams.drawRequired') : this.$t('teams.drawHelp');
    },
    valueError() {
      return this.touched && this.validation === 'valueRequired'
        ? this.$t(`lists.draw.${this.form.unit === 'percent' ? 'percentInvalid' : 'countInvalid'}`) : '';
    },
    daysError() {
      return this.touched && this.validation === 'daysRequired' ? this.$t('lists.draw.daysInvalid') : '';
    },
    will() { return willDraw(this.counts); },
    canCreate() {
      return !!this.counts && this.will > 0 && !!this.form.name.trim() && !this.creating
        && !(this.teamRequired && !this.team);
    },
    createLabel() {
      if (!this.counts) return this.$t('lists.draw.createFirst');
      if (this.will === 0) return this.$t('lists.draw.createNone');
      return this.$t('lists.draw.create', { num: this.$utils.formatNumber(this.will) });
    },
    settingsKey() {
      return JSON.stringify([this.form.unit, this.form.value, this.form.scope, this.form.period, this.form.period === 'days' ? this.form.days : '']);
    },
  },

  watch: {
    settingsKey() {
      this.counts = null;
      this.error = '';
    },
    will(n) {
      if (!this.nameTouched && n > 0) this.form.name = this.defaultName(n);
    },
  },

  methods: {
    num(n) { return this.$t('lists.draw.numFormat', { num: this.$utils.formatNumber(n) }); },

    defaultName(n) {
      return defaultDrawName(this.source.name, this.$t('lists.draw.nameSuffix', {
        date: localDate(), num: this.$utils.formatNumber(n),
      }));
    },

    async check() {
      this.touched = true;
      if (this.validation) return;
      this.checking = true;
      this.error = '';
      const key = this.settingsKey;
      try {
        const data = await this.$api.previewListDraw(this.source.id, drawBody(this.form));
        // Settings that changed while the count was running make it stale.
        if (key === this.settingsKey) this.counts = data;
      } catch (e) {
        this.error = e.response?.data?.message || this.$t('lists.draw.error');
      } finally { this.checking = false; }
    },

    async onSubmit() {
      if (!this.canCreate) return;
      this.creating = true;
      this.error = '';
      try {
        let teamBody = {};
        if (this.isAdmin && this.team === NO_TEAM) teamBody = { no_team: true };
        else if (this.isAdmin && this.team) teamBody = { team: this.team };
        const data = await this.$api.drawList(this.source.id, { ...drawBody(this.form), ...teamBody, name: this.form.name.trim() });
        this.result = { list: data.list, counts: { ...data.draw.result } };
        this.done = true;
        this.$emit('finished');
      } catch (e) {
        this.error = e.response?.data?.message || this.$t('lists.draw.error');
      } finally { this.creating = false; }
    },

    viewList() {
      this.$emit('open-list', this.result.list.id);
      this.$parent.close();
    },

    makeCampaign() {
      this.$router.push(`/campaigns/new?list_id=${this.result.list.id}`);
      this.$parent.close();
    },
  },

  mounted() {
    this.$nextTick(() => { if (this.$refs.focus) this.$refs.focus.focus(); });
  },
});
</script>

<style scoped>
.list-draw { width: 100% !important; max-width: 100%; margin: 0; }
.source-line { margin: 4px 0 0; }
.draw-team { margin: 14px 0 0; }
.draw-row { display: flex; flex-wrap: wrap; gap: 10px; align-items: flex-start; }
.draw-row .field { margin-bottom: 0; }
.draw-days { width: 110px; }
.draw-box { background: #f8fafb; border: 1px solid #dce3e8; border-radius: 5px; padding: 14px 16px; margin: 14px 0; }
.draw-info { margin: 10px 0 0; padding: 8px 12px; border-left: 3px solid #2862b1; background: #f1f6fc; font-size: .85rem; }
.draw-check { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; }
.draw-counts { background: #fff; }
.draw-table { width: 100%; border-collapse: collapse; font-size: .92rem; background: transparent; }
.draw-table td { padding: 5px 0; border: 0; border-bottom: 1px solid #e6ebef; }
.draw-table td:last-child { text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
.draw-table tr.sub td:first-child { padding-left: 18px; color: #555; }
.draw-table tr.result td { border-bottom: 0; font-weight: 700; font-size: 1rem; }
.draw-table tr.result td:last-child { color: #0055d4; }
.draw-note { margin: 10px 0; padding: 8px 12px; font-size: .85rem; border-left: 3px solid; }
.draw-note.is-warning { background: #fff8e1; border-color: #d9a100; color: #5c4400; }
.draw-note.is-danger { background: #fdf0f0; border-color: #d33; color: #7a1d1d; }
.draw-note.is-success { background: #effaf3; border-color: #1f9d55; color: #14502e; font-size: .95rem; }
@media screen and (max-width: 600px) {
  .modal-card-body { padding: 20px 14px; }
}
</style>
