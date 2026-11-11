<template>
  <div class="dynamic-filter" data-cy="dynamic-filter">
    <p class="dynamic-help">{{ $t('lists.dynamic.help') }}</p>
    <b-field :label="$t('lists.dynamic.base')" :message="$t('lists.dynamic.baseHelp')">
      <b-select v-model="form.base.type" expanded data-cy="dynamic-base">
        <option v-if="$can('lists:get_all')" value="all">{{ $t('lists.dynamic.baseAll') }}</option>
        <option value="lists">{{ $t('lists.dynamic.baseLists') }}</option>
      </b-select>
    </b-field>
    <list-selector v-if="form.base.type === 'lists'" :label="$t('lists.dynamic.baseLists')"
      :placeholder="$t('lists.dynamic.selectBase')" :all="ordinaryLists" :selected="selectedBase"
      @input="setBase" />
    <b-field :label="$t('lists.dynamic.match')">
      <b-select v-model="form.match" expanded data-cy="dynamic-match">
        <option value="all">{{ $t('lists.dynamic.matchAll') }}</option>
        <option value="any">{{ $t('lists.dynamic.matchAny') }}</option>
      </b-select>
    </b-field>
    <div v-for="(condition, index) in form.conditions" :key="rowKeys[index]" class="dynamic-condition"
      :data-cy="`dynamic-condition-${index}`">
      <span class="condition-number">{{ index + 1 }}</span>
      <b-field :label="$t('lists.dynamic.key')">
        <b-autocomplete v-model="condition.key" :data="keyOptions(condition.key)" maxlength="64"
          :placeholder="$t('lists.dynamic.keyPlaceholder')" :open-on-focus="true" :keep-first="false"
          @select="key => selectKey(index, key)" :data-cy="`dynamic-key-${index}`" />
      </b-field>
      <b-field :label="$t('lists.dynamic.operator')">
        <b-select v-model="condition.op" expanded :data-cy="`dynamic-op-${index}`" @input="changeOp(index)">
          <option v-for="op in operators" :key="op" :value="op">{{ $t(`lists.dynamic.ops.${op}`) }}</option>
        </b-select>
      </b-field>
      <b-field :label="$t('lists.dynamic.value')">
        <b-input v-if="condition.op !== 'empty'" v-model="condition.value" maxlength="200"
          :placeholder="$t('lists.dynamic.valuePlaceholder')" :data-cy="`dynamic-value-${index}`" />
        <span v-else class="empty-value">{{ $t('lists.dynamic.noValue') }}</span>
      </b-field>
      <b-button type="is-text" icon-left="trash-can-outline" class="condition-remove"
        :aria-label="$t('lists.dynamic.removeCondition', { num: index + 1 })"
        :disabled="form.conditions.length === 1" @click="removeCondition(index)" :data-cy="`dynamic-remove-${index}`" />
    </div>
    <div class="dynamic-buttons">
      <b-button icon-left="plus" :disabled="form.conditions.length >= 20" @click="addCondition" data-cy="dynamic-add">
        {{ $t('lists.dynamic.addCondition') }}
      </b-button>
      <b-button type="is-text" size="is-small" @click="loadKeys" :loading="sampling">
        {{ $t('lists.dynamic.refreshKeys') }}
      </b-button>
    </div>
    <p class="help">{{ $t('lists.dynamic.sampleHelp') }}</p>
    <div class="dynamic-preview" aria-live="polite">
      <b-button @click="preview" :loading="counting" data-cy="dynamic-preview">{{ $t('lists.dynamic.checkCount') }}</b-button>
      <strong v-if="count !== null" data-cy="dynamic-count">{{ $t('lists.dynamic.currentCount', { num: $utils.formatNumber(count) }) }}</strong>
      <span v-else class="has-text-grey">{{ $t('lists.dynamic.countHelp') }}</span>
    </div>
    <p v-if="error" role="alert" class="help is-danger" data-cy="dynamic-error">{{ error }}</p>
    <p class="help consent-help">{{ $t('lists.dynamic.unsubscribeHelp') }}</p>
  </div>
</template>

<script>
import { mapState } from 'vuex';
import ListSelector from './ListSelector.vue';
import { filterToForm, formToFilter, validateDynamicForm } from '../dynamic-list';

export default {
  components: { ListSelector },
  props: {
    value: { type: Object, required: true },
    listId: { type: Number, default: 0 },
  },
  data() {
    return {
      form: filterToForm(this.value),
      operators: ['eq', 'contains', 'empty', 'has'],
      rowKeys: this.value.conditions.map((c, i) => i),
      nextKey: this.value.conditions.length,
      count: null,
      counting: false,
      sampling: false,
      error: '',
      keys: [],
      revision: 0,
      sampleRevision: 0,
    };
  },
  computed: {
    ...mapState(['lists']),
    ordinaryLists() { return (this.lists.results || []).filter((l) => !l.axmFilter); },
    selectedBase() { return this.ordinaryLists.filter((l) => (this.form.base.lists || []).includes(l.id)); },
  },
  watch: {
    form: {
      deep: true,
      handler() {
        this.count = null;
        this.error = '';
        this.revision += 1;
        this.$emit('input', formToFilter(this.form));
      },
    },
    'form.base': {
      deep: true,
      handler() { this.keys = []; this.loadKeys(); },
    },
  },
  methods: {
    setBase(lists) { this.$set(this.form.base, 'lists', lists.map((l) => l.id)); },
    keyOptions(key) { return this.keys.filter((k) => k.toLowerCase().includes(key.toLowerCase())); },
    selectKey(index, key) { if (key) this.form.conditions[index].key = key; },
    changeOp(index) {
      const c = this.form.conditions[index];
      if (c.op === 'empty') this.$delete(c, 'value');
      else if (c.value === undefined) this.$set(c, 'value', '');
      if (c.op === 'has' && !c.key) c.key = 'tags';
    },
    addCondition() {
      this.form.conditions.push({ key: '', op: 'eq', value: '' });
      this.rowKeys.push(this.nextKey);
      this.nextKey += 1;
      this.$nextTick(() => {
        const inputs = this.$el.querySelectorAll('.dynamic-condition .autocomplete input');
        if (inputs.length) inputs[inputs.length - 1].focus();
      });
    },
    removeCondition(index) {
      this.form.conditions.splice(index, 1);
      this.rowKeys.splice(index, 1);
      this.$nextTick(() => this.$el.querySelector('[data-cy="dynamic-add"]').focus());
    },
    async loadKeys() {
      this.sampleRevision += 1;
      const revision = this.sampleRevision;
      this.sampling = true;
      const ids = this.form.base.type === 'lists' ? (this.form.base.lists || []).slice(0, 5) : [null];
      const samples = await Promise.all(ids.map((id) => this.$api.getSubscriberAttribSample({
        ...(id ? { list_id: id } : {}), per_page: Math.floor(50 / ids.length), page: 1, order_by: 'id', order: 'desc',
      }).catch(() => ({ results: [] }))));
      if (revision !== this.sampleRevision) return;
      const keys = new Set();
      samples.forEach((sample) => (sample.results || []).forEach((s) => {
        Object.keys(s.attribs || {}).forEach((key) => { if ([...key].length <= 64) keys.add(key); });
      }));
      this.keys = [...keys].sort();
      this.sampling = false;
    },
    async preview() {
      const invalid = validateDynamicForm(this.form);
      if (invalid) { this.error = this.$t(`lists.dynamic.${invalid}`); return; }
      const { revision } = this;
      this.counting = true;
      this.error = '';
      try {
        const data = await this.$api.previewDynamicList({ filter: formToFilter(this.form), list_id: this.listId });
        if (revision === this.revision) this.count = data.count;
      } catch (e) {
        if (revision === this.revision) this.error = e.response?.data?.message || this.$t('lists.dynamic.previewError');
      } finally { this.counting = false; }
    },
  },
  mounted() {
    if (!this.$can('lists:get_all') && this.form.base.type === 'all') this.form.base = { type: 'lists', lists: [] };
    this.loadKeys();
    if (this.listId) this.preview();
  },
  beforeDestroy() { this.sampleRevision += 1; },
};
</script>

<style scoped>
.dynamic-filter { background: #f8fafb; border: 1px solid #dce3e8; border-radius: 5px; padding: 20px; margin-bottom: 24px; }
.dynamic-help { margin-bottom: 20px; }
.dynamic-condition { display: grid; grid-template-columns: 20px minmax(0, 1.1fr) minmax(0, .9fr) minmax(0, 1.2fr) 36px; gap: 10px; align-items: start; margin: 20px 0 12px; }
.dynamic-condition .field { min-width: 0; margin-bottom: 0; }
.condition-number { padding-top: 33px; color: #777; }
.condition-remove { margin-top: 25px; }
.empty-value { display: block; padding-top: 6px; color: #666; font-size: .85rem; }
.dynamic-buttons { display: flex; flex-wrap: wrap; gap: 8px; }
.dynamic-preview { display: flex; align-items: center; flex-wrap: wrap; gap: 12px; border-top: 1px solid #dce3e8; margin-top: 20px; padding-top: 16px; }
.consent-help { margin-top: 12px; }
@media screen and (max-width: 600px) {
  .dynamic-filter { padding: 14px; }
  .dynamic-condition { grid-template-columns: minmax(0, 1fr) 36px; border-top: 1px solid #dce3e8; padding-top: 12px; }
  .condition-number { padding: 0; grid-column: 1 / -1; }
  .dynamic-condition .field { grid-column: 1; }
  .condition-remove { grid-column: 2; grid-row: 2; }
}
</style>
