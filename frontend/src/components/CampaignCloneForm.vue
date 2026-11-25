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
      <div v-else-if="received.state === 'ok' && received.excluded > 0" class="clone-received is-warn"
        data-cy="clone-received" data-state="warn" role="status">
        <b>{{ $t('campaigns.clone.received', {
          target: $utils.formatNumber(received.target),
          count: $utils.formatNumber(received.excluded),
          name: campaign.name,
          days: DAYS,
        }) }}</b>
        <span class="clone-sub">{{ $t('campaigns.clone.receivedRisk') }}</span>
        <span class="clone-sub" data-cy="clone-received-scope">{{ scopeText }}</span>
        <span class="clone-sub" data-cy="clone-received-way">{{ $t('campaigns.clone.receivedWay') }}</span>
      </div>
      <div v-else-if="received.state === 'ok'" class="clone-received is-note" data-cy="clone-received" data-state="none">
        {{ $t('campaigns.clone.receivedNone', { days: DAYS }) }}
        <span class="clone-sub" data-cy="clone-received-scope">{{ scopeText }}</span>
      </div>
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

    // What the count covers: this one campaign's send records of the last DAYS days.
    scopeText() {
      return this.$t('campaigns.clone.receivedScope', { name: this.campaign.name, days: DAYS });
    },

    isWarn() {
      return this.received.state === 'ok' && this.received.excluded > 0;
    },

    // The button is never held back by the count: only a name and one list are needed.
    canClone() {
      return !this.busy && this.name.trim() !== '' && this.selected.length > 0;
    },
  },

  watch: {
    selectedIds: { handler: 'schedulePreview' },
  },

  async mounted() {
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

    schedulePreview() {
      const ids = this.selectedIds;
      const key = ids.join(',');
      clearTimeout(this.previewTimer);
      if (this.previewController) this.previewController.abort();
      this.previewKey = key;
      this.received = { state: 'none', target: 0, excluded: 0 };
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
      }, PREVIEW_DELAY_MS);
    },

    submit() {
      if (!this.canClone) return;
      this.busy = true;
      this.$emit('clone', { name: this.name.trim(), listIds: this.selectedIds });
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
.clone-form .button.is-primary.clone-go-warn { background-color: #b25e00; }
</style>
