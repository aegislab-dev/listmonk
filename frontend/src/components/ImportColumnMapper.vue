<template>
  <section class="column-mapper" data-cy="map-panel" :aria-label="$t('import.mapTitle')">
    <div class="map-heading">
      <div>
        <h2 class="title is-5">{{ $t('import.mapTitle') }}</h2>
        <p>{{ $t('import.mapIntro') }}</p>
      </div>
      <span class="tag is-light">{{ $t('import.mapLocal') }}</span>
    </div>
    <div class="map-mode">
      <b-radio :value="mode" native-value="mapping" @input="$emit('mode', $event)" data-cy="map-mode-mapping">
        {{ $t('import.mapMode') }}
      </b-radio>
      <b-radio :value="mode" native-value="raw" :disabled="!rawAvailable" @input="$emit('mode', $event)" data-cy="map-mode-raw">
        {{ $t('import.mapRaw') }}
      </b-radio>
    </div>
    <p v-if="encoding !== 'utf-8'" class="help">{{ $t('import.mapEncodingRequired') }}</p>
    <div class="map-controls">
      <b-field :label="$t('import.mapEncoding')">
        <b-select :value="encodingChoice" @input="$emit('encoding', $event)" data-cy="map-encoding">
          <option value="auto">{{ $t('import.mapAuto') }}</option>
          <option value="utf-8">UTF-8</option>
          <option value="cp949">CP949</option>
        </b-select>
      </b-field>
      <p>{{ $t('import.mapDetected', { encoding: encoding === 'cp949' ? 'CP949' : (encoding === 'utf-8' ? 'UTF-8' : '—'), count: total === null ? '—' : total }) }}</p>
      <b-checkbox :value="header" @input="$emit('header', $event)" data-cy="map-header">{{ $t('import.mapHeader') }}</b-checkbox>
    </div>
    <p v-if="busy" class="help" role="status" data-cy="map-busy">{{ $t('import.mapBusy') }}</p>
    <b-message v-if="headerHint" type="is-warning" :closable="false">{{ $t('import.mapHeaderHint') }}</b-message>
    <b-message v-if="error" type="is-danger" :closable="false" data-cy="map-error">{{ error }}</b-message>
    <b-message v-if="mode === 'raw'" type="is-info" :closable="false" data-cy="map-raw-info">{{ $t('import.mapRawHelp') }}</b-message>
    <template v-else>
      <template v-if="columns.length">
      <div class="map-status-bar" data-cy="map-status" aria-live="polite">
        <strong>{{ $t('import.mapListTitle', { columns: columns.length }) }}</strong>
        <span class="map-chip is-ok" data-cy="map-count-ok">{{ $t('import.mapCountOk', { count: counts.ok }) }}</span>
        <span v-if="counts.skipped" class="map-chip is-skipped" data-cy="map-count-skipped">{{ $t('import.mapCountSkipped', { count: counts.skipped }) }}</span>
        <button v-if="counts.error" type="button" class="map-chip is-error" data-cy="map-count-error" @click="focusFirstError">
          {{ $t('import.mapCountError', { count: counts.error }) }} ↓
        </button>
      </div>
      <p v-for="(message, n) in validation" :key="n" class="map-global-error" role="alert" data-cy="map-validation">! {{ message }}</p>
      <div class="map-list-head" aria-hidden="true">
        <span>{{ $t('import.mapColumn') }}</span><span>{{ $t('import.mapRole') }}</span><span>{{ $t('import.mapState') }}</span>
      </div>
      <ul class="map-list" data-cy="map-list">
        <li v-for="row in rows" :key="row.index" class="map-row" :class="`is-${row.state}`" :data-cy="`map-row-${row.index}`">
          <div class="map-row-source">
            <div class="map-row-name">{{ row.label }}</div>
            <div class="map-row-samples">
              <span v-for="(sample, n) in row.samples" :key="n" class="map-sample" :title="sample">{{ sample }}</span>
              <span v-if="!row.samples.length" class="map-sample-empty">{{ $t('import.mapNoSamples') }}</span>
            </div>
          </div>
          <div class="map-row-target">
            <b-select :id="`map-role-${row.index}`" :value="mapping[row.index].role"
              :aria-label="$t('import.mapRoleFor', { column: row.label })" :data-cy="`map-role-${row.index}`"
              @input="change(row.index, 'role', $event)">
              <option value="email">{{ $t('import.mapEmail') }}</option>
              <option value="name">{{ $t('import.mapName') }}</option>
              <option value="attribute">{{ $t('import.mapAttribute') }}</option>
              <option value="tags">{{ $t('import.mapTags') }}</option>
              <option value="discard">{{ $t('import.mapDiscard') }}</option>
            </b-select>
            <b-autocomplete v-if="['attribute', 'tags'].includes(mapping[row.index].role)" :id="`map-key-${row.index}`"
              :value="mapping[row.index].key" :data="keyOptions(mapping[row.index].key)" :open-on-focus="true"
              :clear-on-select="false" :placeholder="$t('import.mapKeyPlaceholder')" dropdown-position="bottom"
              :aria-label="$t('import.mapKeyFor', { column: row.label })" :data-cy="`map-key-${row.index}`"
              :aria-invalid="row.state === 'error'" @input="change(row.index, 'key', $event)" @select="selectKey(row.index, $event)" />
          </div>
          <div class="map-row-state" :data-cy="`map-state-${row.index}`">
            <span>{{ row.text }}</span>
            <button v-if="row.fix" type="button" class="map-fix" :data-cy="`map-fix-${row.index}`" @click="change(row.index, 'key', row.fix)">
              {{ $t('import.mapUseKey', { key: row.fix }) }}
            </button>
          </div>
        </li>
      </ul>
      <p class="help map-key-help">{{ $t('import.mapKeyHelp') }}</p>
      </template>
      <b-message v-if="overwrite" type="is-warning" :closable="false" data-cy="map-overwrite">{{ $t('import.mapOverwrite') }}</b-message>
      <section v-if="summary" class="map-review" data-cy="map-summary" aria-live="polite">
        <h3 class="title is-6">{{ $t('import.mapReview') }}</h3>
        <div class="map-counts">
          <div><strong>{{ summary.imported }}</strong><span>{{ $t('import.mapReady') }}</span></div>
          <div :class="{ 'has-text-warning-dark': excluded > 0 }"><strong>{{ excluded }}</strong><span>{{ $t('import.mapExcluded') }}</span></div>
          <div><strong>{{ summary.cleaned }}</strong><span>{{ $t('import.mapCleaned') }}</span></div>
        </div>
        <div class="map-issues" v-if="excluded">
          <p v-for="kind in ['empty', 'invalid', 'duplicate', 'malformed']" :key="kind">
            <template v-if="summary[kind]">
              <strong>{{ $t(`import.mapIssue${kind}`, { count: summary[kind] }) }}</strong>
              <span>{{ $t('import.mapRows') }} {{ issueExamples(kind) }}</span>
            </template>
          </p>
        </div>
        <p class="help">{{ $t('import.mapDuplicateHelp') }}</p>
      </section>
      <section v-if="preview.length" class="map-preview" data-cy="map-preview">
        <h3 class="title is-6">{{ $t('import.mapPreview') }}</h3>
        <p class="help">{{ $t('import.mapPreviewHelp') }}</p>
        <div class="map-preview-scroll" tabindex="0" :aria-label="$t('import.mapPreview')">
          <table class="table is-fullwidth">
            <thead><tr><th>{{ $t('import.mapEmail') }}</th><th>{{ $t('import.mapName') }}</th><th>{{ $t('import.mapAttribute') }}</th></tr></thead>
            <tbody>
              <tr v-for="row in preview" :key="row.row">
                <td>{{ row.email }}</td>
                <td>{{ row.name || '—' }}</td>
                <td>
                  <span v-for="[key, value] in row.attributes" :key="key" class="map-attr"><b>{{ key }}</b> {{ Array.isArray(value) ? value.join(', ') : value }}</span>
                  <template v-if="!row.attributes.length">—</template>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
      <p class="help map-remember">{{ $t('import.mapRemember') }}</p>
    </template>
  </section>
</template>

<script>
import { suggestKey, isSpelledKey } from '../lib/csv-import';

// One source column per row; parsing and conversion run in the parent’s Worker.
export default {
  props: {
    busy: { type: Boolean, default: false },
    mode: { type: String, default: 'mapping' },
    rawAvailable: { type: Boolean, default: false },
    encoding: { type: String, default: 'utf-8' },
    encodingChoice: { type: String, default: 'auto' },
    header: { type: Boolean, default: true },
    headerHint: { type: Boolean, default: false },
    columns: { type: Array, default: () => [] },
    samples: { type: Array, default: () => [] },
    columnErrors: { type: Object, default: () => ({}) },
    columnWarnings: { type: Object, default: () => ({}) },
    mapping: { type: Array, default: () => [] },
    existingKeys: { type: Array, default: () => [] },
    validation: { type: Array, default: () => [] },
    total: { type: Number, default: 0 },
    summary: { type: Object, default: null },
    examples: { type: Object, default: () => ({}) },
    preview: { type: Array, default: () => [] },
    error: { type: String, default: '' },
    overwrite: { type: Boolean, default: false },
  },
  computed: {
    rows() {
      return this.columns.map((label, index) => {
        const { role, key } = this.mapping[index];
        const named = ['attribute', 'tags'].includes(role);
        let state = 'ok';
        let text = this.$t(role === 'tags' ? 'import.mapTagsHelp' : 'import.mapStateOk');
        let fix = '';
        if (this.columnErrors[index]) {
          state = 'error';
          text = this.columnErrors[index];
          // Offer a ready-made name instead of explaining the naming rule.
          if (named) fix = suggestKey(key || label);
          if (fix === key) fix = '';
        } else if (role === 'discard') {
          state = 'skipped';
          text = this.$t('import.mapStateSkipped');
        } else if (this.columnWarnings[index]) {
          state = 'warning';
          text = this.columnWarnings[index];
        } else if (role === 'attribute' && !this.existingKeys.includes(key) && isSpelledKey(label, key)) {
          text = this.$t('import.mapStateAuto');
        }
        return {
          index, label, state, text, fix, samples: (this.samples[index] || []).filter((sample) => sample.trim()),
        };
      });
    },
    counts() {
      const count = (...states) => this.rows.filter(({ state }) => states.includes(state)).length;
      return { ok: count('ok', 'warning'), skipped: count('skipped'), error: count('error') };
    },
    excluded() {
      return ['empty', 'invalid', 'duplicate', 'malformed'].reduce((sum, key) => sum + this.summary[key], 0);
    },
  },
  methods: {
    focusFirstError() {
      const { index } = this.rows.find(({ state }) => state === 'error');
      const row = this.$el.querySelector(`[data-cy=map-row-${index}]`);
      row.scrollIntoView({ block: 'center', behavior: 'smooth' });
      row.querySelector(`#map-key-${index}, #map-role-${index}`).focus({ preventScroll: true });
    },
    change(index, field, value) {
      this.$emit('mapping', this.mapping.map((entry, n) => (n === index ? { ...entry, [field]: value } : entry)));
    },
    selectKey(index, key) {
      // Deselecting an old suggestion must not erase the new text being typed.
      if (typeof key === 'string') this.change(index, 'key', key);
    },
    keyOptions(query) {
      return this.existingKeys.filter((key) => key.toLowerCase().includes(query.toLowerCase()));
    },
    issueExamples(kind) {
      return (this.examples[kind] || []).map(({ row, email }) => `${row}${email ? ` (${email})` : ''}`).join(', ');
    },
  },
};
</script>
