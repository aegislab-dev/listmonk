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
      <b-radio :value="mode" native-value="raw" :disabled="encoding !== 'utf-8'" @input="$emit('mode', $event)" data-cy="map-mode-raw">
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
      <p>{{ $t('import.mapDetected', { encoding: encoding === 'cp949' ? 'CP949' : 'UTF-8', count: total }) }}</p>
      <b-checkbox :value="header" @input="$emit('header', $event)" data-cy="map-header">{{ $t('import.mapHeader') }}</b-checkbox>
    </div>
    <b-message v-if="headerHint" type="is-warning" :closable="false">{{ $t('import.mapHeaderHint') }}</b-message>
    <b-message v-if="error" type="is-danger" :closable="false" data-cy="map-error">{{ error }}</b-message>
    <b-message v-if="mode === 'raw'" type="is-info" :closable="false" data-cy="map-raw-info">{{ $t('import.mapRawHelp') }}</b-message>
    <template v-else>
      <p class="help map-key-help">{{ $t('import.mapKeyHelp') }}</p>
      <div class="map-sheet-caption">
        <strong>{{ $t('import.mapSheetTitle', { columns: columns.length, rows: sampleRows.length }) }}</strong>
        <div class="map-sheet-navigation">
          <b-button size="is-small" :aria-label="$t('import.mapPreviousColumn')" @click="scrollSheet(-1)">←</b-button>
          <b-button size="is-small" :aria-label="$t('import.mapNextColumn')" @click="scrollSheet(1)">→</b-button>
        </div>
      </div>
      <p class="help map-scroll-help">{{ $t('import.mapSheetHelp') }}</p>
      <div ref="sheet" class="map-sheet-scroll" tabindex="0" :aria-label="$t('import.mapSheetLabel')" data-cy="map-sheet">
        <table class="table map-sheet">
          <colgroup><col class="map-row-number" /><col v-for="(_, index) in columns" :key="index" /></colgroup>
          <thead>
            <tr class="map-sheet-letters">
              <th class="map-sheet-corner" scope="col" aria-label="#" />
              <th v-for="(_, index) in columns" :key="index" scope="col" :class="columnClass(index)">{{ columnLetter(index) }}</th>
            </tr>
            <tr class="map-sheet-assignments">
              <th class="map-row-label" scope="row">{{ header ? 1 : '—' }}</th>
              <th v-for="(column, index) in columns" :key="index" scope="col" :class="columnClass(index)">
                <div class="map-source-header">{{ column || $t('import.mapUnnamed', { num: index + 1 }) }}</div>
                <label class="map-cell-label" :for="`map-role-${index}`">{{ $t('import.mapRole') }}</label>
                <b-select :id="`map-role-${index}`" :value="mapping[index].role" expanded
                  :aria-label="$t('import.mapRoleFor', { column: column || index + 1 })" :data-cy="`map-role-${index}`"
                  @input="change(index, 'role', $event)">
                  <option value="">{{ $t('import.mapUnassigned') }}</option>
                  <option value="email">{{ $t('import.mapEmail') }}</option>
                  <option value="name">{{ $t('import.mapName') }}</option>
                  <option value="attribute">{{ $t('import.mapAttribute') }}</option>
                  <option value="tags">{{ $t('import.mapTags') }}</option>
                  <option value="discard">{{ $t('import.mapDiscard') }}</option>
                </b-select>
                <div class="map-key-cell">
                  <template v-if="['attribute', 'tags'].includes(mapping[index].role)">
                    <label class="map-cell-label" :for="`map-key-${index}`">{{ $t('import.mapKey') }}</label>
                    <b-autocomplete :id="`map-key-${index}`" :value="mapping[index].key" :data="keyOptions(mapping[index].key)"
                      :open-on-focus="true" :clear-on-select="false" :placeholder="$t('import.mapKeyPlaceholder')" dropdown-position="bottom"
                      :aria-label="$t('import.mapKeyFor', { column: column || index + 1 })" :data-cy="`map-key-${index}`"
                      :aria-invalid="!!columnErrors[index]" @input="change(index, 'key', $event)" @select="change(index, 'key', $event || '')" />
                    <span class="help" v-if="mapping[index].role === 'tags'">{{ $t('import.mapTagsHelp') }}</span>
                  </template>
                  <span v-else class="map-field-target">{{ fieldTarget(mapping[index].role) }}</span>
                </div>
                <p v-if="columnErrors[index]" class="map-column-error" :data-cy="`map-column-error-${index}`">! {{ columnErrors[index] }}</p>
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(row, rowIndex) in sampleRows" :key="rowIndex">
              <th class="map-row-label" scope="row">{{ sampleRowNumbers[rowIndex] || rowIndex + (header ? 2 : 1) }}</th>
              <td v-for="(sample, index) in row" :key="index" :class="columnClass(index)">{{ sample || '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <b-message v-if="validation.length" type="is-danger" :closable="false" data-cy="map-validation">
        <p v-for="(message, n) in validation" :key="n">{{ message }}</p>
      </b-message>
      <b-message v-if="overwrite" type="is-warning" :closable="false" data-cy="map-overwrite">{{ $t('import.mapOverwrite') }}</b-message>
      <section v-if="summary && !validation.length" class="map-review" data-cy="map-summary" aria-live="polite">
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
      <section v-if="preview.length && !validation.length" class="map-preview" data-cy="map-preview">
        <h3 class="title is-6">{{ $t('import.mapPreview') }}</h3>
        <p class="help">{{ $t('import.mapPreviewHelp') }}</p>
        <div class="map-preview-scroll" tabindex="0" :aria-label="$t('import.mapPreview')">
          <table class="table is-fullwidth">
<thead><tr><th>email</th><th>name</th><th>attributes</th></tr></thead>
            <tbody><tr v-for="row in preview" :key="row.row"><td>{{ row.email }}</td><td>{{ row.name || '—' }}</td><td><code>{{ row.attributes || '—' }}</code></td></tr></tbody>
          </table>
        </div>
      </section>
      <p class="help map-remember">{{ $t('import.mapRemember') }}</p>
    </template>
  </section>
</template>

<script>
// Presentation shell for UI approval. Worker and upload wiring follow approval.
export default {
  props: {
    mode: { type: String, default: 'mapping' },
    encoding: { type: String, default: 'utf-8' },
    encodingChoice: { type: String, default: 'auto' },
    header: { type: Boolean, default: true },
    headerHint: { type: Boolean, default: false },
    columns: { type: Array, default: () => [] },
    samples: { type: Array, default: () => [] },
    sampleRowNumbers: { type: Array, default: () => [] },
    columnErrors: { type: Object, default: () => ({}) },
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
    sampleRows() {
      const count = Math.min(3, Math.max(0, ...this.samples.map((column) => column.length)));
      return Array.from({ length: count }, (_, row) => this.columns.map((label, column) => this.samples[column]?.[row] || ''));
    },
    excluded() {
      return ['empty', 'invalid', 'duplicate', 'malformed'].reduce((sum, key) => sum + this.summary[key], 0);
    },
  },
  methods: {
    columnLetter(index) {
      let value = index + 1;
      let label = '';
      while (value > 0) {
        value -= 1;
        label = String.fromCharCode(65 + (value % 26)) + label;
        value = Math.floor(value / 26);
      }
      return label;
    },
    columnClass(index) {
      const { role, key } = this.mapping[index];
      return {
        'map-column-muted': !role || role === 'discard' || (['attribute', 'tags'].includes(role) && !key),
        'map-column-has-error': !!this.columnErrors[index],
      };
    },
    scrollSheet(direction) {
      this.$refs.sheet.scrollBy({ left: 220 * direction, behavior: 'smooth' });
    },
    change(index, field, value) {
      this.$emit('mapping', this.mapping.map((entry, n) => (n === index ? { ...entry, [field]: value } : entry)));
    },
    keyOptions(query) {
      return this.existingKeys.filter((key) => key.toLowerCase().includes(query.toLowerCase()));
    },
    fieldTarget(role) {
      if (role === 'email') return 'email';
      if (role === 'name') return 'name';
      return this.$t('import.mapNotSaved');
    },
    issueExamples(kind) {
      return (this.examples[kind] || []).map(({ row, email }) => `${row}${email ? ` (${email})` : ''}`).join(', ');
    },
  },
};
</script>
