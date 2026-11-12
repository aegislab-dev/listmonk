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
          @select="key => selectKey(index, key)" :data-cy="`dynamic-key-${index}`">
          <template #default="props">
            <span class="key-option">
              <span>{{ props.option }}</span>
              <span v-if="standardLabel(props.option)" class="key-option-std">{{ standardLabel(props.option) }} · {{ $t('lists.dynamic.standardTag') }}</span>
            </span>
          </template>
        </b-autocomplete>
      </b-field>
      <b-field :label="$t('lists.dynamic.operator')">
        <b-select v-model="condition.op" expanded :data-cy="`dynamic-op-${index}`" @input="changeOp(index)">
          <optgroup :label="$t('lists.dynamic.opsText')">
            <option v-for="op in textOps" :key="op" :value="op">{{ $t(`lists.dynamic.ops.${op}`) }}</option>
          </optgroup>
          <optgroup :label="$t('lists.dynamic.opsNumber')">
            <option v-for="op in numberOps" :key="op" :value="op">{{ $t(`lists.dynamic.ops.${op}`) }}</option>
          </optgroup>
        </b-select>
      </b-field>
      <div v-if="isNumberOp(condition.op)" class="condition-value" role="group" :aria-label="$t('lists.dynamic.value')">
        <span class="label" aria-hidden="true">{{ $t('lists.dynamic.value') }}</span>
        <div class="number-values">
          <div v-for="field in numberFields(condition.op)" :key="field" class="number-value">
            <div class="number-input">
              <b-input v-model="condition[field]" inputmode="decimal" autocomplete="off" maxlength="64"
                :has-counter="false" :custom-class="numberError(index, field) ? 'is-danger' : ''"
                :placeholder="$t('lists.dynamic.numberPlaceholder')"
                :aria-label="$t(`lists.dynamic.numberLabel.${condition.op === 'between' ? field : 'single'}`)"
                @blur="blurNumber(index, field)" :data-cy="`dynamic-${field}-${index}`" />
              <span v-if="condition.op === 'between'" class="number-suffix" aria-hidden="true">
                {{ $t(`lists.dynamic.range.${field}`) }}
              </span>
            </div>
            <p v-if="numberError(index, field)" class="help is-danger" role="alert"
              :data-cy="`dynamic-${field}-error-${index}`">
              {{ numberError(index, field) }}
            </p>
            <p v-else-if="numberHint(condition[field])" class="help number-hint"
              :data-cy="`dynamic-${field}-hint-${index}`">
              {{ numberHint(condition[field]) }}
            </p>
          </div>
        </div>
      </div>
      <b-field v-else :label="$t('lists.dynamic.value')" :addons="false">
        <b-input v-if="condition.op !== 'empty'" v-model="condition.value" maxlength="200"
          :placeholder="$t('lists.dynamic.valuePlaceholder')" :data-cy="`dynamic-value-${index}`" />
        <span v-else class="empty-value">{{ $t('lists.dynamic.noValue') }}</span>
        <p v-if="countryHint(condition)" class="help country-hint" :data-cy="`dynamic-country-hint-${index}`">
          {{ $t('lists.dynamic.countryHint') }}
          <button v-if="countryHint(condition).code" type="button" class="country-use" :data-cy="`dynamic-country-use-${index}`"
            @click="condition.value = countryHint(condition).code">
            {{ $t('lists.dynamic.countryUse', { code: countryHint(condition).code }) }}
          </button>
          <template v-else>{{ $t('lists.dynamic.countryExample') }}</template>
        </p>
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
    <p v-if="hasNumberCondition" class="help" data-cy="dynamic-number-help">{{ $t('lists.dynamic.numberHelp') }}</p>
    <div class="dynamic-summary" data-cy="dynamic-summary" aria-live="polite">
      <strong>{{ $t('lists.dynamic.summaryLabel') }}</strong>
      <p v-if="summary">{{ summary }}</p>
      <p v-else class="has-text-grey">{{ $t('lists.dynamic.summaryEmpty') }}</p>
    </div>
    <div class="dynamic-preview" aria-live="polite">
      <b-button @click="preview" :loading="counting" data-cy="dynamic-preview">{{ $t('lists.dynamic.checkCount') }}</b-button>
      <strong v-if="count !== null" data-cy="dynamic-count">{{ $t('lists.dynamic.currentCount', { num: $utils.formatNumber(count) }) }}</strong>
      <span v-else class="has-text-grey">{{ $t('lists.dynamic.countHelp') }}</span>
    </div>
    <div v-if="count !== null" class="dynamic-result">
      <p v-if="excludedText" class="excluded" data-cy="dynamic-excluded">{{ excludedText }}</p>
      <p v-if="notNumber > 0" class="not-number" data-cy="dynamic-not-number">
        {{ $t('lists.dynamic.notNumber', { num: $utils.formatNumber(notNumber) }) }}
      </p>
      <p v-if="sample === null" class="help" data-cy="dynamic-sample-denied">{{ $t('lists.dynamic.sampleDenied') }}</p>
      <div v-else-if="sample.length" class="dynamic-sample" data-cy="dynamic-sample">
        <p class="sample-title">{{ $t('lists.dynamic.sampleTitle', { num: sample.length }) }}</p>
        <div class="sample-scroll">
          <table class="table is-narrow is-fullwidth">
            <thead>
              <tr>
                <th>{{ $t('subscribers.email') }}</th>
                <th>{{ $t('globals.fields.name') }}</th>
                <th v-for="key in sampleKeys" :key="key">{{ key }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in sample" :key="row.id">
                <td class="sample-email">{{ row.email }}</td>
                <td>{{ row.name }}</td>
                <td v-for="key in sampleKeys" :key="key" class="sample-attrib" :data-label="key">
                  <span v-if="attribText(row, key) === null" class="has-text-grey">{{ $t('lists.dynamic.sampleNoValue') }}</span>
                  <template v-else>{{ attribText(row, key) }}</template>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <p class="sample-more">
          <span v-if="count > sample.length">{{ $t('lists.dynamic.sampleMore', { num: $utils.formatNumber(count - sample.length) }) }} · </span>
          <router-link v-if="isSaved" :to="`/subscribers/lists/${listId}`" data-cy="dynamic-sample-all">
            {{ $t('lists.dynamic.sampleViewAll') }}
          </router-link>
          <span v-else class="has-text-grey">{{ $t('lists.dynamic.sampleSaveHint') }}</span>
        </p>
      </div>
    </div>
    <p v-if="error" role="alert" class="help is-danger" data-cy="dynamic-error">{{ error }}</p>
    <p class="help consent-help">{{ $t('lists.dynamic.unsubscribeHelp') }}</p>
  </div>
</template>

<script>
import { mapState } from 'vuex';
import ListSelector from './ListSelector.vue';
import {
  filterToForm, formatNumberText, formToFilter, isNumberOp, koreanNumberText, NUMBER_OPS, parseNumberText, summarizeFilter,
  TEXT_OPS, validateDynamicForm,
} from '../dynamic-list';
import { countryHint, standardByKey, withStandardFirst } from '../lib/standard-attribs';

export default {
  components: { ListSelector },
  props: {
    value: { type: Object, required: true },
    listId: { type: Number, default: 0 },
  },
  data() {
    return {
      form: filterToForm(this.value),
      textOps: TEXT_OPS,
      numberOps: NUMBER_OPS,
      // Number inputs the user has left, keyed by row key and field; errors show only for these.
      touched: {},
      notNumber: 0,
      excluded: {},
      // Members shown with the count; null when the user may not read subscribers.
      sample: [],
      sampleKeys: [],
      // The filter as saved, to offer the full member list only while nothing has changed.
      savedFilter: this.listId ? JSON.stringify(formToFilter(filterToForm(this.value))) : '',
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
    ...mapState(['lists', 'serverConfig']),
    // The standard attribute dictionary: its items are offered first, sampled or not.
    standardAttribs() { return this.serverConfig.axm_standard_attribs || []; },
    ordinaryLists() { return (this.lists.results || []).filter((l) => !l.axmFilter); },
    selectedBase() { return this.ordinaryLists.filter((l) => (this.form.base.lists || []).includes(l.id)); },
    hasNumberCondition() { return this.form.conditions.some((c) => isNumberOp(c.op)); },
    summary() {
      const names = Object.fromEntries(this.ordinaryLists.map((l) => [l.id, l.name]));
      return summarizeFilter(this.form, names, (key, params) => this.$t(key, params), (key) => this.$te(key));
    },
    excludedText() {
      const items = [['unsubscribed', 'excludedUnsubscribed'], ['blocklisted', 'excludedBlocklisted'], ['optedOut', 'excludedOptedOut']]
        .filter(([field]) => this.excluded[field] > 0)
        .map(([field, key]) => this.$t(`lists.dynamic.${key}`, { num: this.$utils.formatNumber(this.excluded[field]) }));
      return items.length ? this.$t('lists.dynamic.excluded', { items: items.join(' · ') }) : '';
    },
    isSaved() { return !!this.savedFilter && this.savedFilter === JSON.stringify(formToFilter(this.form)); },
  },
  watch: {
    form: {
      deep: true,
      handler() {
        this.count = null;
        this.notNumber = 0;
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
    keyOptions(key) {
      return withStandardFirst(this.standardAttribs, this.keys).filter((k) => k.toLowerCase().includes(key.toLowerCase()));
    },
    standardLabel(key) { return standardByKey(this.standardAttribs, key) ? this.$t(`subscribers.standard.${key}`) : ''; },
    // "같음" compares the stored text exactly, and a country is stored as its code.
    countryHint(c) { return c.op === 'eq' ? countryHint(this.standardAttribs, c.key, c.value) : null; },
    selectKey(index, key) { if (key) this.form.conditions[index].key = key; },
    isNumberOp,
    numberFields(op) { return op === 'between' ? ['value', 'value2'] : ['value']; },
    changeOp(index) {
      const c = this.form.conditions[index];
      if (c.op === 'empty') this.$delete(c, 'value');
      else if (c.value === undefined) this.$set(c, 'value', '');
      if (c.op !== 'between') this.$delete(c, 'value2');
      else if (c.value2 === undefined) this.$set(c, 'value2', '');
      // A value typed for a text condition is checked as a number right away.
      if (isNumberOp(c.op) && c.value) this.blurNumber(index, 'value');
      if (c.op === 'has' && !c.key) c.key = 'tags';
    },
    blurNumber(index, field) {
      const c = this.form.conditions[index];
      this.$set(this.touched, `${this.rowKeys[index]}:${field}`, true);
      const text = formatNumberText(c[field]);
      if (text !== c[field]) c[field] = text;
    },
    numberError(index, field) {
      const c = this.form.conditions[index];
      const text = (c[field] || '').trim();
      if (!text || !this.touched[`${this.rowKeys[index]}:${field}`]) return '';
      if (parseNumberText(text) === null) return this.$t('lists.dynamic.numberInvalid');
      if (field === 'value2' && parseNumberText(c.value) !== null
        && !(Number(parseNumberText(c.value)) < Number(parseNumberText(text)))) return this.$t('lists.dynamic.rangeInvalid');
      return '';
    },
    // The value of a condition's item for a sampled member; null when the member has none.
    attribText(row, key) {
      const v = (row.attribs || {})[key];
      if (v === undefined || v === null || v === '') return null;
      return typeof v === 'string' ? v : JSON.stringify(v);
    },
    // Korean reads large numbers in units of 10,000, which catches a missing or extra zero.
    numberHint(text) { return this.$i18n.locale === 'ko' ? koreanNumberText(text) : ''; },
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
        if (revision === this.revision) {
          this.count = data.count;
          this.notNumber = data.notNumber || 0;
          this.excluded = data.excluded || {};
          this.sample = data.sample || null;
          this.sampleKeys = [...new Set(this.form.conditions.map((c) => c.key.trim()).filter(Boolean))];
        }
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
.dynamic-condition .field, .condition-value { min-width: 0; margin-bottom: 0; }
.condition-number { padding-top: 33px; color: #777; }
.condition-remove { margin-top: 25px; }
.empty-value { display: block; padding-top: 6px; color: #666; font-size: .85rem; }
.key-option { display: flex; justify-content: space-between; align-items: center; gap: 10px; }
.key-option-std { border: 1px solid #b9d3f0; border-radius: 3px; background: #f2f7fd; color: #1d5a9e; padding: 0 5px; font-size: .7rem; white-space: nowrap; }
.country-hint { color: #5c4400; }
.country-use { border: 0; background: none; padding: 0; color: #0055d4; font: inherit; font-weight: 600; text-decoration: underline; cursor: pointer; }
.condition-value .label { display: block; margin-bottom: .5em; }
.number-values { display: grid; gap: 8px; }
.number-value { min-width: 0; }
.number-input { display: flex; align-items: center; gap: 8px; }
.number-input .control { flex: 1; min-width: 0; }
.number-suffix { flex: none; color: #444; }
.number-hint { color: #666; }
.dynamic-summary { border-top: 1px solid #dce3e8; margin-top: 20px; padding-top: 16px; }
.dynamic-summary p { margin: 4px 0 0; }
.dynamic-preview { display: flex; align-items: center; flex-wrap: wrap; gap: 12px; margin-top: 16px; }
.dynamic-result { margin-top: 12px; }
.dynamic-result > * + * { margin-top: 10px; }
.excluded { margin: 0; color: #444; font-size: .9rem; }
.dynamic-sample { background: #fff; border: 1px solid #dce3e8; border-radius: 5px; padding: 12px; }
.sample-title { margin: 0 0 8px; font-weight: 600; font-size: .9rem; }
.sample-scroll { overflow-x: auto; }
.sample-scroll table { margin: 0; font-size: .85rem; background: transparent; }
.sample-scroll th, .sample-scroll td { white-space: nowrap; }
.sample-more { margin: 8px 0 0; font-size: .85rem; }
.not-number { margin: 0; padding: 8px 12px; border-left: 3px solid #d9a100; background: #fff8e1; color: #5c4400; font-size: .85rem; }
.dynamic-buttons { display: flex; flex-wrap: wrap; gap: 8px; }
.consent-help { margin-top: 12px; }
@media screen and (max-width: 600px) {
  .dynamic-filter { padding: 14px; }
  .dynamic-condition { grid-template-columns: minmax(0, 1fr) 36px; border-top: 1px solid #dce3e8; padding-top: 12px; }
  .condition-number { padding: 0; grid-column: 1 / -1; }
  .dynamic-condition .field, .condition-value { grid-column: 1; }
  .condition-remove { grid-column: 2; grid-row: 2; }
  /* One member per block, so the values the conditions read stay on screen. */
  .sample-scroll thead { display: none; }
  .sample-scroll tr { display: block; padding: 8px 0; border-bottom: 1px solid #e6ebef; }
  .sample-scroll tr:last-child { border-bottom: 0; }
  .sample-scroll td { display: block; border: 0; padding: 1px 0; white-space: normal; overflow-wrap: anywhere; }
  .sample-email { font-weight: 600; }
  .sample-attrib::before { content: attr(data-label) ": "; color: #666; }
}
</style>
