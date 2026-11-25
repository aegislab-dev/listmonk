<template>
  <form class="modal-card clone-form" style="width: auto" data-cy="clone-form" @submit.prevent="submit">
    <header class="modal-card-head">
      <h4 class="clone-title">{{ $t('campaigns.clone.title') }}</h4>
      <button type="button" class="delete clone-close" data-cy="clone-close"
        :aria-label="$t('globals.buttons.close')" @click="$emit('close')" />
    </header>

    <section class="modal-card-body">
      <b-field :label="$t('globals.fields.name')" label-position="on-border">
        <b-input v-model="name" name="name" maxlength="200" data-cy="clone-name" required />
      </b-field>

      <div data-cy="clone-lists">
        <list-selector v-model="selected" :selected="selected" :all="allLists"
          :label="$t('campaigns.clone.lists')" :placeholder="$t('campaigns.clone.listPlaceholder')" />
      </div>
      <p class="help" data-cy="clone-lists-help">
        {{ $t(prefill ? 'campaigns.clone.helpPrefilled' : 'campaigns.clone.help') }}
      </p>

      <!-- The original's lists, written out but never filled in unless the person asks. -->
      <div class="clone-orig" data-cy="clone-original">
        <div class="clone-orig-text">
          <div>{{ $t('campaigns.clone.original', { name: campaign.name }) }}</div>
          <div v-for="l in originalLists" :key="l.id" class="clone-orig-list">
            <b>{{ l.name }}</b>
            <span v-if="l.count !== null" class="clone-nowrap"> · {{ $t('campaigns.recent.people', { count: $utils.formatNumber(l.count) }) }}</span>
          </div>
        </div>
        <b-button size="is-small" type="is-primary is-light" class="clone-same" data-cy="clone-same-lists"
          :disabled="!originalFillable" @click="fillOriginal">
          {{ $t('campaigns.clone.sameLists') }}
        </b-button>
      </div>
      <p v-if="listsError" class="help is-danger" data-cy="clone-lists-error">{{ $t('campaigns.clone.listsError') }}</p>

      <!-- How many of the people in the chosen lists already got the original (LST-34's
           preview, read only). It never blocks the copy. -->
      <div v-if="received.state === 'loading'" class="clone-received is-note" data-cy="clone-received" data-state="loading">
        {{ $t('campaigns.clone.checking') }}
      </div>
      <div v-else-if="received.state === 'error'" class="clone-received is-note" data-cy="clone-received" data-state="error">
        {{ $t('campaigns.clone.checkFailed') }}
      </div>
      <div v-else-if="received.state === 'ok' && eff === 'none' && received.excluded > 0" class="clone-received is-warn"
        data-cy="clone-received" data-state="warn" role="status">
        <b>{{ $t('campaigns.clone.received', {
          target: $utils.formatNumber(received.target),
          count: $utils.formatNumber(received.excluded),
          name: campaign.name,
          days: DAYS,
        }) }}</b>
        <span class="clone-sub">{{ $t('campaigns.clone.receivedRisk') }}</span>
      </div>
      <div v-else-if="eff !== 'none' && ruleNote" class="clone-received is-note" data-cy="clone-received" data-state="rule" role="status">
        <b>{{ ruleNote }}</b>
        <span v-if="restNote" class="clone-sub" data-cy="clone-rule-rest">{{ restNote }}</span>
      </div>

      <!-- LST-65: the rule that leaves out the people who got a campaign lately now works when the
           copy is sent. The copy takes the original's rule, leaves out the people who got the
           original, or goes without one. -->
      <div v-if="ruleState === 'error'" class="clone-received is-note" data-cy="clone-rule-error" role="status">
        {{ $t('campaigns.clone.ruleLoadFailed') }}
      </div>

      <fieldset v-if="showRule" class="clone-rule" data-cy="clone-rule">
        <legend class="clone-rule-title">{{ $t('campaigns.clone.ruleTitle') }}</legend>
        <label v-if="original.enabled" class="clone-radio">
          <input v-model="choice" type="radio" value="inherit" data-cy="clone-rule-inherit" @change="choiceTouched = true">
          <span>
            {{ $t('campaigns.clone.ruleInherit', { days: original.days, scope: $t(`campaigns.recent.scope.${original.scope}`) }) }}
            <small>{{ $t('campaigns.clone.ruleInheritHelp') }}</small>
          </span>
        </label>
        <label class="clone-radio">
          <input v-model="choice" type="radio" value="exclude" data-cy="clone-rule-exclude" @change="choiceTouched = true">
          <span>
            {{ $t('campaigns.clone.ruleExclude') }}
            <small>
              {{ received.state === 'ok'
                ? $t('campaigns.clone.ruleExcludeHelpCount', { name: campaign.name, days: DAYS, count: $utils.formatNumber(received.excluded) })
                : $t('campaigns.clone.ruleExcludeHelp', { name: campaign.name, days: DAYS }) }}
            </small>
          </span>
        </label>
        <label class="clone-radio">
          <input v-model="choice" type="radio" value="none" data-cy="clone-rule-none" @change="choiceTouched = true">
          <span>
            {{ $t('campaigns.clone.ruleNone') }}
            <small v-if="!original.enabled && ruleState === 'ok'">{{ $t('campaigns.clone.ruleNoneHelp') }}</small>
          </span>
        </label>
      </fieldset>
    </section>

    <footer class="modal-card-foot">
      <b-button data-cy="btn-clone-cancel" @click="$emit('close')">{{ $t('globals.buttons.cancel') }}</b-button>
      <b-button native-type="submit" type="is-primary" :class="{ 'clone-go-warn': isWarn }"
        :disabled="!canClone" data-cy="btn-clone-confirm">
        {{ $t(isWarn ? 'campaigns.clone.goAsIs' : 'globals.buttons.clone') }}
      </b-button>
    </footer>
  </form>
</template>

<script>
import ListSelector from './ListSelector.vue';
import { recentRulePayload } from '../recent-send-exclude';

// Where the copy's lists start (LST-59). false: empty, the person picks them (the original's
// lists are one click away). true: the original's lists are filled in at the start. This is
// the one place to change it.
const PREFILL_ORIGINAL_LISTS = false;

// The look-back of the "already received" count: LST-34's preview offers 7, 14 and 30.
const DAYS = 30;
const PREVIEW_DELAY_MS = 400;

export default {
  name: 'CampaignCloneForm',

  components: { ListSelector },

  props: {
    // The campaign row to copy: id, name, type, lists [{ id, name }].
    campaign: { type: Object, required: true },
  },

  data() {
    return {
      DAYS,
      prefill: PREFILL_ORIGINAL_LISTS,
      name: this.$t('campaigns.copyOf', { name: this.campaign.name }),
      selected: [],
      allLists: [],
      listsError: false,
      received: { state: 'none', target: 0, excluded: 0 },
      // The original's recent-send rule (LST-65) and what the copy does with it: 'inherit' (take
      // it), 'exclude' (leave out whoever got the original) or 'none'. The first one is the start
      // when the original has a rule.
      // ruleState: 'loading' until the original's rule is read, then 'ok' or 'error' (the copy waits for it).
      ruleState: this.campaign.type === 'regular' ? 'loading' : 'ok',
      ruleOffered: false,
      original: { enabled: false, days: 14, scope: 'all', campaignIds: [] },
      choice: 'none',
      choiceTouched: false,
      inherited: { state: 'none', excluded: 0, target: 0 },
      previewTimer: null,
      previewController: null,
      previewKey: '',
      busy: false,
    };
  },

  computed: {
    // The original's lists the person may see, with their size when the full list knows it.
    originalLists() {
      return (this.campaign.lists || []).map((l) => {
        const full = this.allLists.find((a) => a.id === l.id);
        return { id: l.id, name: l.name, count: full && Number.isFinite(full.subscriberCount) ? full.subscriberCount : null };
      });
    },

    originalFillable() {
      return this.allLists.length > 0 && this.campaign.lists.some((l) => this.allLists.some((a) => a.id === l.id));
    },

    selectedIds() {
      return this.selected.map((l) => l.id);
    },

    // What the copy does: the choice, while it is offered; otherwise nothing.
    eff() { return this.showRule ? this.choice : 'none'; },

    isWarn() {
      return this.eff === 'none' && this.received.state === 'ok' && this.received.excluded > 0;
    },

    // The choice is offered when the original has a rule, or when people in the lists got the
    // original lately (there is something to leave out).
    // Once offered it stays: a count that is read again (lists changed) must not take a choice away.
    showRule() {
      if (this.campaign.type !== 'regular') return false;
      return this.ruleOffered || this.original.enabled || (this.received.state === 'ok' && this.received.excluded > 0);
    },

    // What the copy will do, as the box says it.
    ruleNote() {
      if (this.eff === 'exclude' && this.received.state === 'ok') {
        return this.$t('campaigns.clone.noteExclude', {
          name: this.campaign.name, days: DAYS, count: this.$utils.formatNumber(this.received.excluded),
        });
      }
      if (this.eff === 'inherit') {
        const scope = this.$t(`campaigns.recent.scope.${this.original.scope}`);
        if (this.inherited.state === 'ok') {
          return this.$t('campaigns.clone.noteInherit', {
            days: this.original.days, scope, count: this.$utils.formatNumber(this.inherited.excluded),
          });
        }
        return this.$t('campaigns.clone.noteInheritNoCount', { days: this.original.days, scope });
      }
      return '';
    },

    restNote() {
      let src = null;
      if (this.eff === 'exclude' && this.received.state === 'ok') src = this.received;
      if (this.eff === 'inherit' && this.inherited.state === 'ok') src = this.inherited;
      if (!src) return '';
      return this.$t('campaigns.clone.noteRest', {
        target: this.$utils.formatNumber(src.target), rest: this.$utils.formatNumber(src.target - src.excluded),
      });
    },

    // The button is never held back by the count: only a name and one list are needed.
    canClone() {
      return !this.busy && this.ruleState !== 'loading' && this.name.trim() !== '' && this.selected.length > 0;
    },
  },

  watch: {
    selectedIds: { handler: 'schedulePreview' },
    showRule(on) { if (on) this.ruleOffered = true; },
  },

  async mounted() {
    this.loadRule();
    try {
      const data = await this.$api.queryLists({ minimal: false, per_page: 'all', status: 'active' });
      this.allLists = (data.results || []).map((l) => ({ ...l, id: l.id, name: l.name }));
    } catch (e) {
      this.listsError = true;
    }
    if (this.prefill) this.fillOriginal();
  },

  beforeDestroy() {
    clearTimeout(this.previewTimer);
    if (this.previewController) this.previewController.abort();
  },

  methods: {
    fillOriginal() {
      // Only the lists the person can see: the server filters the rest out of a copy too.
      this.selected = this.campaign.lists
        .map((l) => this.allLists.find((a) => a.id === l.id))
        .filter(Boolean)
        .map(({ id, name }) => ({ id, name }));
    },

    // The original's rule: absent or unreadable is "no rule" (the copy is never held back by it).
    async loadRule() {
      if (this.campaign.type !== 'regular') return;
      try {
        const rule = await this.$api.getRecentRule(this.campaign.id);
        if (rule && rule.enabled) {
          this.original = {
            enabled: true, days: rule.days, scope: rule.scope, campaignIds: rule.campaignIds || [],
          };
          // Starts on the original's rule, unless the person chose before it was read.
          if (!this.choiceTouched) this.choice = 'inherit';
        }
        this.ruleState = 'ok';
        this.schedulePreview();
      } catch (e) {
        // The rule could not be read: the copy goes without one, and the window says so.
        this.ruleState = 'error';
      }
    },

    schedulePreview() {
      const ids = this.selectedIds;
      const key = ids.join(',');
      clearTimeout(this.previewTimer);
      if (this.previewController) this.previewController.abort();
      this.previewKey = key;
      this.received = { state: 'none', target: 0, excluded: 0 };
      this.inherited = { state: 'none', excluded: 0, target: 0 };
      // The count is the original's, so a campaign that is not a regular one has none.
      if (!ids.length || this.campaign.type !== 'regular') return;

      this.received = { state: 'loading', target: 0, excluded: 0 };
      const controller = new AbortController();
      this.previewController = controller;
      this.previewTimer = setTimeout(async () => {
        const current = () => !controller.signal.aborted && this.previewKey === key;
        try {
          const out = await this.$api.previewRecentExclude({
            list_ids: ids, days: DAYS, scope: 'campaigns', campaign_ids: [this.campaign.id], campaign_id: 0,
          }, controller.signal);
          if (current()) this.received = { state: 'ok', target: out.target, excluded: out.excluded };
        } catch (e) {
          if (current()) this.received = { state: 'error', target: 0, excluded: 0 };
        }
        // How many the original's own rule would leave out of the chosen lists (the copy has no
        // id yet, so none of the sends is the copy's own).
        if (this.original.enabled) {
          try {
            const out = await this.$api.previewRecentExclude({
              list_ids: ids, ...recentRulePayload({ ...this.original, enabled: true }), campaign_id: 0,
            }, controller.signal);
            if (current()) this.inherited = { state: 'ok', target: out.target, excluded: out.excluded };
          } catch (e) {
            if (current()) this.inherited = { state: 'error', excluded: 0, target: 0 };
          }
        }
      }, PREVIEW_DELAY_MS);
    },

    submit() {
      if (!this.canClone) return;
      this.busy = true;
      // The rule the copy gets (LST-65), saved after the copy is made; null: none.
      let rule = null;
      if (this.eff === 'inherit') {
        rule = recentRulePayload({ ...this.original, enabled: true });
      } else if (this.eff === 'exclude') {
        rule = recentRulePayload({
          enabled: true, days: DAYS, scope: 'campaigns', campaignIds: [this.campaign.id],
        });
      }
      this.$emit('clone', { name: this.name.trim(), listIds: this.selectedIds, rule });
    },
  },
};
</script>

<style scoped>
.clone-form { max-width: 520px; }
.clone-form .modal-card-foot { background: #fff; border-top: 1px solid #ededed; border-radius: 0 0 6px 6px; justify-content: flex-end; gap: 0.5rem; }
.clone-title { font-size: 1.25rem; font-weight: 700; }
.clone-form .modal-card-head { display: flex; flex-direction: row; justify-content: space-between; align-items: center; }
.clone-close { flex: none; margin-left: 1rem; }
.clone-orig { display: flex; gap: 0.75rem; justify-content: space-between; align-items: flex-start; margin-top: 0.75rem; padding: 0.5rem 0.75rem; background: #f5f5f5; border-radius: 6px; font-size: 0.875rem; }
.clone-orig-text { min-width: 0; overflow-wrap: anywhere; }
.clone-same { flex: none; }
.clone-nowrap { white-space: nowrap; }
.clone-received { margin-top: 0.75rem; padding: 0.5rem 0.75rem; border-radius: 6px; font-size: 0.875rem; }
.clone-received.is-note { background: #f5f5f5; color: #6b6b6b; }
.clone-received.is-warn { background: #fffaeb; border: 1px solid #ffe08a; color: #7a4a00; }
.clone-sub { display: block; margin-top: 0.25rem; font-size: 0.8rem; }
.clone-rule { margin-top: 0.75rem; padding: 0.5rem 0.75rem; border: 1px solid #e3e3e3; border-radius: 6px; font-size: 0.875rem; }
.clone-rule-title { font-weight: 600; padding: 0 0.25rem; }
.clone-radio { display: flex; gap: 0.5rem; align-items: flex-start; margin-top: 0.4rem; cursor: pointer; line-height: 1.35; }
.clone-radio input { margin-top: 0.25rem; }
.clone-radio small { display: block; color: #6b6b6b; font-size: 0.8rem; }
.clone-form .button.is-primary.clone-go-warn { background-color: #b25e00; }
</style>
