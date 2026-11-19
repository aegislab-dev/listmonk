<template>
  <section v-if="listIds.length" class="ac" :class="stateClass" data-cy="address-check">
    <button type="button" class="ac-head" :aria-expanded="open ? 'true' : 'false'" :disabled="!canOpen"
      data-cy="address-toggle" @click="open = !open">
      <span class="ac-dot" aria-hidden="true" />
      <span class="ac-title">{{ $t('addressCheck.title') }}</span>
      <span class="ac-summary" role="status" data-cy="address-summary">
        <template v-if="error">{{ error }}</template>
        <template v-else-if="!result">{{ $t('addressCheck.checking') }}</template>
        <template v-else-if="!result.checkedLists">{{ $t('addressCheck.noAccess') }}</template>
        <template v-else-if="result.tooLarge">
          {{ $t('addressCheck.tooLarge', { total: n(result.total), limit: n(result.limit) }) }}
        </template>
        <template v-else-if="!result.risky">
          {{ $t(skipped ? 'addressCheck.cleanPartial' : 'addressCheck.clean', { total: n(result.total) }) }}<template v-if="tail"> · {{ tail }}</template>
        </template>
        <template v-else>
          {{ $t(skipped ? 'addressCheck.targetPartial' : 'addressCheck.target', { total: n(result.total) }) }}
          <b>{{ $t('addressCheck.risky', { count: n(result.risky) }) }}</b>
          {{ $t('addressCheck.breakdown', { confirmed: n(result.confirmed), warning: n(result.warning) }) }}<template v-if="tail"> · {{ tail }}</template>
        </template>
      </span>
      <span v-if="canOpen" class="ac-toggle">{{ $t(open ? 'addressCheck.collapse' : 'addressCheck.expand') }} {{ open ? '▴' : '▾' }}</span>
    </button>
    <p v-if="scopeNote" class="ac-scope" data-cy="address-scope">{{ scopeNote }}</p>
    <p v-if="showSkipped" class="ac-scope is-partial" data-cy="address-partial">
      {{ $t('addressCheck.partial', { count: n(skipped) }) }}
    </p>

    <div v-if="open && canOpen" class="ac-body" data-cy="address-body">
      <div v-for="group in groups" :key="group.kind" class="ac-group" :data-cy="`address-group-${group.kind}`">
        <p class="ac-group-title">
          <span class="ac-chip" :class="`is-${group.kind}`">{{ $t(`addressCheck.group.${group.kind}`, { count: n(group.count) }) }}</span>
          <small>{{ $t(`addressCheck.groupHelp.${group.kind}`) }}</small>
        </p>
        <div v-for="row in group.rows" :key="row.name" class="ac-row" :data-cy="`address-row-${row.name}`">
          <div class="ac-row-main">
            <span class="ac-row-name">{{ $t(`addressCheck.class.${row.name}`) }}</span>
            <span class="ac-row-count">{{ $t('addressCheck.people', { count: n(row.count) }) }}</span>
            <p class="ac-row-desc">
              {{ $t(`addressCheck.classHelp.${row.name}`) }}
              <template v-if="row.sample">
                <code>{{ row.sample }}</code>
                <template v-if="row.count > 1"> {{ $t('addressCheck.more', { count: n(row.count - 1) }) }}</template>
              </template>
            </p>
          </div>
          <div v-if="row.sample" class="ac-row-actions">
            <button type="button" class="ac-link" :data-cy="`address-list-${row.name}`" @click="shown = row.name">
              {{ $t('addressCheck.showList') }}
            </button>
            <template v-if="canUnsubscribe && group.kind !== 'unknown' && unsubIds(row.name).length">
              <b-button v-if="group.kind === 'confirmed'" size="is-small" type="is-danger" outlined :loading="busy === row.name"
                :disabled="!!busy" :data-cy="`address-unsub-${row.name}`" @click="askUnsubscribe(row.name)">
                {{ $t('addressCheck.unsubscribe') }}
              </b-button>
              <button v-else type="button" class="ac-link is-quiet" :disabled="!!busy" :data-cy="`address-unsub-${row.name}`"
                @click="askUnsubscribe(row.name)">
                {{ $t('addressCheck.unsubscribe') }}…
              </button>
            </template>
          </div>
        </div>
      </div>

      <p class="ac-note" data-cy="address-note">
        {{ $t('addressCheck.noBlock') }}
        <template v-if="result.detail && $can('subscribers:manage')">
          {{ $t(targetIds.length ? 'addressCheck.unsubscribeNote' : 'addressCheck.dynamicOnly') }}
          <template v-if="targetIds.length && hasDynamic"> {{ $t('addressCheck.dynamicMixed') }}</template>
          <template v-if="targetIds.length && linkedText"> {{ $t('addressCheck.linked', { lists: linkedText }) }}</template>
        </template>
        <template v-else-if="!result.detail"> {{ $t('addressCheck.noDetail') }}</template>
      </p>
      <p class="ac-foot">
        <span>{{ $t('addressCheck.dnsOnly') }}</span>
        <button type="button" class="ac-link" :disabled="loading" data-cy="address-recheck" @click="recheck">
          {{ $t('addressCheck.recheck') }}
        </button>
      </p>
    </div>

    <b-modal :active="!!shown" has-modal-card scroll="keep" :aria-modal="true" :width="640" @close="shown = ''">
      <div v-if="shown" class="modal-card content ac-modal" style="width: auto" data-cy="address-modal">
        <header class="modal-card-head">
          <h4 class="title is-size-5">
            {{ $t(`addressCheck.class.${shown}`) }} {{ $t('addressCheck.people', { count: n(result.classes[shown].count) }) }}
          </h4>
        </header>
        <section class="modal-card-body">
          <p class="ac-row-desc">{{ $t(`addressCheck.classHelp.${shown}`) }}</p>
          <p v-if="result.classes[shown].truncated" class="ac-row-desc" data-cy="address-truncated">
            {{ $t('addressCheck.truncated', { count: n(shownRows.length) }) }}
          </p>
          <div class="ac-table-scroll">
            <table class="table is-fullwidth is-narrow ac-table">
              <thead><tr><th>{{ $t('subscribers.email') }}</th><th>{{ $t('addressCheck.classes') }}</th></tr></thead>
              <tbody>
                <tr v-for="row in shownRows" :key="row.id">
                  <td>{{ row.email }}</td>
                  <td>
                    <span v-for="c in row.classes" :key="c" class="ac-chip" :class="`is-${kindOf(c)}`">
                      {{ $t(`addressCheck.class.${c}`) }}
                    </span>
                    <small v-if="canUnsubscribe && !row.direct" class="ac-indirect">{{ $t('addressCheck.indirect') }}</small>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
        <footer class="modal-card-foot">
          <b-button @click="shown = ''">{{ $t('globals.buttons.close') }}</b-button>
          <b-button v-if="canUnsubscribe && shown !== 'unknown' && unsubIds(shown).length" type="is-danger" outlined
            :disabled="!!busy" data-cy="address-modal-unsub" @click="askUnsubscribe(shown)">
            {{ $t('addressCheck.unsubscribeCount', { count: n(unsubIds(shown).length) }) }}
          </b-button>
        </footer>
      </div>
    </b-modal>
  </section>
</template>

<script>
// Shows the addresses of the chosen lists that are likely to bounce, and lets people
// unsubscribe them from those lists. It never blocks a send. See FORK.md (LST-43).
const CONFIRMED = ['format', 'dead', 'nullmx'];
const WARNING = ['disposable', 'role'];
// While DNS lookups are unfinished the check is asked again, this often and this many times.
const POLL_MS = 3000;
const MAX_POLLS = 10;

export default {
  props: {
    listIds: { type: Array, required: true },
    // The list objects ({ id, name, axmFilter }) the IDs can belong to.
    lists: { type: Array, default: () => [] },
    scopeNote: { type: String, default: '' },
    // Lists of the same send that were left out before the check was asked (the import
    // status gives only the lists this user may see). They count as not checked.
    hiddenLists: { type: Number, default: 0 },
  },

  data() {
    return {
      result: null,
      loading: false,
      error: '',
      open: false,
      shown: '',
      busy: '',
      polls: 0,
      timer: null,
      controller: null,
    };
  },

  computed: {
    key() {
      return [...new Set(this.listIds)].sort((a, b) => a - b).join(',');
    },

    polling() {
      return !!this.result && this.result.pending > 0 && this.polls < MAX_POLLS;
    },

    // What is not known (yet): being looked up, or given up on.
    tail() {
      const r = this.result;
      if (this.polling) return this.$t('addressCheck.pending', { count: this.n(r.pending) });
      const unknown = r.unknown + r.pending;
      return unknown ? this.$t('addressCheck.unknown', { count: this.n(unknown) }) : '';
    },

    // How many lists of this send were not checked because the user cannot see them. Their
    // recipients are not in the counts, so "no risky addresses" would say too much.
    skipped() {
      if (!this.result) return 0;
      return (this.result.uncheckedLists || 0) + this.hiddenLists;
    },

    showSkipped() {
      const r = this.result;
      return !this.error && !!r && !!r.checkedLists && !r.tooLarge && this.skipped > 0;
    },

    stateClass() {
      if (this.error || !this.result || this.result.tooLarge || !this.result.checkedLists) return 'is-busy';
      if (this.result.risky) return 'is-risk';
      return this.skipped ? 'is-busy' : 'is-ok';
    },

    canOpen() {
      const r = this.result;
      return !!r && !this.error && !r.tooLarge && !!r.checkedLists && (r.risky > 0 || r.unknown > 0 || r.pending > 0);
    },

    groups() {
      const { classes } = this.result;
      const rows = (names) => names.filter((name) => classes[name] && classes[name].count > 0).map((name) => ({
        name, count: classes[name].count, sample: (classes[name].samples || [])[0] || '',
      }));
      return [
        { kind: 'confirmed', count: this.result.confirmed, rows: rows(CONFIRMED) },
        { kind: 'warning', count: this.result.warning, rows: rows(WARNING) },
        { kind: 'unknown', count: this.result.unknown, rows: rows(['unknown']) },
      ].filter((group) => group.rows.length);
    },

    // Where an unsubscribe can change something: the chosen ordinary lists the user may manage,
    // as the server reports them. A dynamic list has no rows of its own.
    targetIds() {
      return (this.result && this.result.targets) || [];
    },

    targetNames() {
      return this.targetIds.map((id) => (this.lists.find((l) => l.id === id) || { name: `#${id}` }).name).join(', ');
    },

    // Some chosen list is not a target (dynamic, or not manageable by this user).
    hasDynamic() {
      return [...new Set(this.listIds)].some((id) => !this.targetIds.includes(id));
    },

    canUnsubscribe() {
      return this.result.detail && this.$can('subscribers:manage') && this.targetIds.length > 0;
    },

    // Every listed address of the shown class, with all the classes the server found for it.
    shownRows() {
      const cls = this.result.classes[this.shown];
      return (cls.ids || []).map((id, i) => ({
        id, email: cls.emails[i], classes: (cls.tags || [])[i] || [this.shown], direct: !cls.direct || cls.direct[i],
      }));
    },

    // The other lists an unsubscribe here also reaches because a draw ties them (from the server).
    linkedText() {
      const names = this.result.linked || [];
      const more = Math.max(0, names.length - 5) + (this.result.linkedHidden || 0);
      const shown = names.slice(0, 5).join(', ');
      if (!shown) return more > 0 ? this.$t('addressCheck.linkedHidden', { count: this.n(more) }) : '';
      return more > 0 ? this.$t('addressCheck.linkedMore', { lists: shown, count: this.n(more) }) : shown;
    },
  },

  watch: {
    key: { immediate: true, handler: 'schedule' },
  },

  beforeDestroy() {
    clearTimeout(this.timer);
    this.controller?.abort();
  },

  methods: {
    n(count) {
      return Number(count).toLocaleString(this.$i18n.locale);
    },

    kindOf(name) {
      if (CONFIRMED.includes(name)) return 'confirmed';
      return WARNING.includes(name) ? 'warning' : 'unknown';
    },

    // A new set of lists: forget the old answer and ask after a short pause.
    schedule() {
      clearTimeout(this.timer);
      this.controller?.abort();
      this.result = null;
      this.error = '';
      this.shown = '';
      this.polls = 0;
      this.loading = false;
      if (!this.key) return;
      this.timer = setTimeout(this.load, 500);
    },

    recheck() {
      clearTimeout(this.timer);
      this.polls = 0;
      this.load();
    },

    async load() {
      const { key } = this;
      this.controller?.abort();
      const controller = new AbortController();
      this.controller = controller;
      this.loading = true;
      try {
        const result = await this.$api.checkListAddresses({ list_ids: key.split(',').map(Number) }, controller.signal);
        if (controller.signal.aborted || this.key !== key) return;
        this.result = result;
        this.error = '';
        if (this.shown && !(result.classes[this.shown] && result.classes[this.shown].ids)) this.shown = '';
        if (this.polling) {
          this.polls += 1;
          this.timer = setTimeout(this.load, POLL_MS);
        }
      } catch (err) {
        if (controller.signal.aborted || this.key !== key) return;
        this.error = err.response?.data?.message || this.$t('addressCheck.failed');
      } finally {
        if (!controller.signal.aborted && this.key === key) this.loading = false;
      }
    },

    // The listed people of a class who have a subscription of their own in the chosen ordinary
    // lists. Someone who is in only through a dynamic list cannot be unsubscribed here.
    unsubIds(name) {
      const cls = this.result.classes[name] || {};
      return (cls.ids || []).filter((id, i) => !cls.direct || cls.direct[i]);
    },

    askUnsubscribe(name) {
      const ids = this.unsubIds(name);
      if (!ids.length || this.busy) return;
      const count = this.n(ids.length);
      const lists = this.targetNames;
      const warning = [
        this.$t('addressCheck.confirm.body', { lists, count }),
        this.linkedText ? this.$t('addressCheck.confirm.linked', { lists: this.linkedText }) : '',
        WARNING.includes(name) ? this.$t(`addressCheck.confirm.${name}`) : '',
        this.result.classes[name].truncated ? this.$t('addressCheck.confirm.truncated') : '',
        this.$t('addressCheck.confirm.undo'),
      ].filter(Boolean).join(' ');
      this.$utils.confirmWarn(
        this.$t('addressCheck.confirm.title', { name: this.$t(`addressCheck.class.${name}`), count }),
        warning,
        this.$t('addressCheck.unsubscribeCount', { count }),
        () => this.unsubscribe(name, ids),
      );
    },

    async unsubscribe(name, ids) {
      this.busy = name;
      try {
        await this.$api.addSubscribersToLists({ ids, action: 'unsubscribe', target_list_ids: this.targetIds });
        this.$utils.toast(this.$t('addressCheck.unsubscribed', { count: this.n(ids.length) }));
        this.shown = '';
        this.$emit('changed');
        this.recheck();
      } catch (err) {
        // The API layer has shown the error.
      } finally {
        this.busy = '';
      }
    },
  },
};
</script>

<style scoped>
.ac { border: 1px solid #dbdbdb; border-radius: 6px; margin-bottom: 20px; font-size: .875rem; text-align: left; background: #fff; }
.ac-head { display: flex; align-items: center; gap: 10px; width: 100%; padding: 13px 16px; border: 0; background: none; font: inherit; color: inherit; text-align: left; cursor: pointer; }
.ac-head:disabled { cursor: default; }
.ac-dot { flex: none; width: 10px; height: 10px; border-radius: 50%; background: #b5b5b5; }
.ac.is-risk .ac-dot { background: #d9822b; }
.ac.is-ok .ac-dot { background: #2e9e5b; }
.ac-title { flex: none; font-weight: 600; }
.ac-summary { flex: 1; min-width: 0; }
.ac-summary b { font-weight: 700; }
.ac.is-risk .ac-summary b { color: #8a4b08; }
.ac.is-ok .ac-summary { color: #1c6b3f; }
.ac-toggle { flex: none; color: #0055d4; font-size: .8125rem; white-space: nowrap; }
.ac-scope { margin: -6px 16px 12px 36px; color: #616161; font-size: .8125rem; }
.ac-scope + .ac-scope { margin-top: -8px; }
.ac-scope.is-partial { color: #614600; font-weight: 600; }
.ac-body { border-top: 1px solid #e9e9e9; padding: 2px 16px 14px; }
.ac-group { margin-top: 14px; }
.ac-group-title { display: flex; align-items: baseline; gap: 8px; margin-bottom: 2px; }
.ac-group-title small { color: #616161; font-size: .8125rem; }
.ac-chip { display: inline-block; border-radius: 999px; padding: 1px 10px; margin-right: 4px; font-size: .75rem; font-weight: 600; line-height: 1.7; white-space: nowrap; }
.ac-chip.is-confirmed { background: #fff0f1; color: #852330; }
.ac-chip.is-warning { background: #fff5db; color: #614600; }
.ac-chip.is-unknown { background: #f1f1f1; color: #4a4a4a; }
.ac-row { display: flex; align-items: center; gap: 12px; padding: 9px 0; border-bottom: 1px solid #efefef; }
.ac-row:last-child { border-bottom: 0; }
.ac-row-main { flex: 1; min-width: 0; }
.ac-row-name { font-weight: 600; }
.ac-row-count { margin-left: 6px; font-variant-numeric: tabular-nums; color: #363636; }
.ac-row-desc { margin: 0; color: #616161; font-size: .8125rem; overflow-wrap: anywhere; }
.ac-row-desc code { background: none; color: #363636; padding: 0; font-size: .8125rem; }
.ac-row-actions { flex: none; display: flex; align-items: center; gap: 4px; }
.ac-link { border: 0; background: none; padding: 4px 6px; font: inherit; font-size: .8125rem; color: #0055d4; cursor: pointer; }
.ac-link:disabled { color: #9a9a9a; cursor: default; }
.ac-link.is-quiet { color: #616161; text-decoration: underline; }
.ac-note { margin-top: 12px; border-radius: 4px; padding: 8px 10px; font-size: .8125rem; background: #eef2fc; color: #263675; }
.ac-foot { display: flex; justify-content: space-between; align-items: center; gap: 12px; margin-top: 8px; color: #616161; font-size: .75rem; }
.ac-modal .modal-card-head .title { margin: 0; }
.ac-modal .modal-card-body { padding: 8px 20px 12px; }
.ac-table-scroll { max-height: 55vh; overflow-y: auto; margin-top: 10px; }
.ac-indirect { color: #616161; white-space: nowrap; }
.ac-table td, .ac-table th { font-size: .875rem; vertical-align: middle; overflow-wrap: anywhere; }
@media (max-width: 480px) {
  .ac-head { flex-wrap: wrap; }
  .ac-row { flex-direction: column; align-items: flex-start; gap: 4px; }
}
</style>
