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
      <table class="table is-fullwidth map-columns">
        <thead>
<tr>
          <th>{{ $t('import.mapColumn') }}</th><th>{{ $t('import.mapSamples') }}</th>
          <th>{{ $t('import.mapRole') }}</th><th>{{ $t('import.mapKey') }}</th>
        </tr>
</thead>
        <tbody>
          <tr v-for="(column, index) in columns" :key="index" :class="{ 'map-discard': mapping[index].role === 'discard' }">
            <th scope="row"><span class="map-column-number">{{ index + 1 }}</span>{{ column || $t('import.mapUnnamed', { num: index + 1 }) }}</th>
            <td :data-label="$t('import.mapSamples')" class="map-samples">
              <span v-for="(sample, n) in samples[index]" :key="n">{{ sample || '—' }}</span>
            </td>
            <td :data-label="$t('import.mapRole')">
              <b-select :value="mapping[index].role" expanded :aria-label="$t('import.mapRoleFor', { column: column || index + 1 })"
                :data-cy="`map-role-${index}`" @input="change(index, 'role', $event)">
                <option value="email">{{ $t('import.mapEmail') }}</option>
                <option value="name">{{ $t('import.mapName') }}</option>
                <option value="attribute">{{ $t('import.mapAttribute') }}</option>
                <option value="tags">{{ $t('import.mapTags') }}</option>
                <option value="discard">{{ $t('import.mapDiscard') }}</option>
              </b-select>
            </td>
            <td :data-label="$t('import.mapKey')">
              <template v-if="['attribute', 'tags'].includes(mapping[index].role)">
                <b-autocomplete :value="mapping[index].key" :data="keyOptions(mapping[index].key)" :open-on-focus="true"
                  :clear-on-select="false" :placeholder="$t('import.mapKeyPlaceholder')" dropdown-position="bottom"
                  :aria-label="$t('import.mapKeyFor', { column: column || index + 1 })" :data-cy="`map-key-${index}`"
                  @input="change(index, 'key', $event)" @select="change(index, 'key', $event || '')" />
                <span class="help" v-if="mapping[index].role === 'tags'">{{ $t('import.mapTagsHelp') }}</span>
              </template>
              <span v-else class="map-field-target">{{ fieldTarget(mapping[index].role) }}</span>
            </td>
          </tr>
        </tbody>
      </table>
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
    excluded() {
      return ['empty', 'invalid', 'duplicate', 'malformed'].reduce((sum, key) => sum + this.summary[key], 0);
    },
  },
  methods: {
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
