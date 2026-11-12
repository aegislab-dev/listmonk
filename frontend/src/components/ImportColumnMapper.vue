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
                <!-- No whitespace inside: the chip's text is exactly the cell (and what it becomes). -->
                <span v-for="(sample, n) in row.samples" :key="n" class="map-sample" :class="{ 'is-unreadable': sample.unreadable }"
                  :title="sample.to ? `${sample.text} → ${sample.to}` : sample.text">{{ sample.text }}<template v-if="sample.to"> <i aria-hidden="true">→</i> <b>{{ sample.to }}</b></template></span>
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
              <div v-if="['attribute', 'tags'].includes(mapping[row.index].role)" class="map-row-key">
                <b-autocomplete :id="`map-key-${row.index}`"
                  :value="mapping[row.index].key" :data="keyOptions(mapping[row.index].key)" :open-on-focus="true"
                  :clear-on-select="false" :placeholder="$t('import.mapKeyPlaceholder')" dropdown-position="bottom"
                  :aria-label="$t('import.mapKeyFor', { column: row.label })" :data-cy="`map-key-${row.index}`"
                  :aria-invalid="row.state === 'error'" @input="change(row.index, 'key', $event)" @select="selectKey(row.index, $event)">
                  <template #default="props">
                    <span class="map-option">
                      <span>{{ props.option }}</span>
                      <span v-if="standardLabel(props.option)" class="map-option-std">{{ standardLabel(props.option) }} · {{ $t('import.mapStdTag') }}</span>
                    </span>
                  </template>
                </b-autocomplete>
                <p v-if="row.standard" class="map-row-note" :data-cy="`map-std-${row.index}`">
                  <span class="map-std-tag">{{ $t('import.mapStdTag') }}</span>
                  {{ row.standard.label }} · {{ $t(`import.mapStdKind.${row.standard.kind}`) }}
                </p>
                <p v-else-if="row.twin" class="map-row-note" :data-cy="`map-twin-${row.index}`">
                  {{ $t('import.mapTwin', { key: row.twin.key, label: row.twin.label }) }}
                  <button type="button" class="map-link" :data-cy="`map-twin-use-${row.index}`" @click="change(row.index, 'key', row.twin.key)">
                    {{ $t('import.mapUseKey', { key: row.twin.key }) }}
                  </button>
                </p>
              </div>
            </div>
            <div class="map-row-state" :data-cy="`map-state-${row.index}`">
              <span>{{ row.text }}</span>
              <button v-if="row.fix" type="button" class="map-fix" :data-cy="`map-fix-${row.index}`" @click="change(row.index, 'key', row.fix)">
                {{ $t('import.mapUseKey', { key: row.fix }) }}
              </button>
              <template v-if="row.unreadable">
                <p class="map-unreadable" :data-cy="`map-unreadable-${row.index}`">{{ row.unreadable.examples }}</p>
                <template v-if="!row.unreadable.blank">
                  <p class="map-unreadable-help">{{ $t(`import.mapUnreadableHelp.${row.unreadable.kind}`) }}</p>
                  <button type="button" class="map-fix" :data-cy="`map-blank-${row.index}`" @click="change(row.index, 'blank', true)">
                    {{ $t('import.mapBlankUnreadable') }}
                  </button>
                  <button type="button" class="map-fix" :data-cy="`map-rename-${row.index}`" @click="renameKey(row.index)">
                    {{ $t('import.mapRenameKey') }}
                  </button>
                </template>
                <button v-else type="button" class="map-link" :data-cy="`map-unblank-${row.index}`" @click="change(row.index, 'blank', false)">
                  {{ $t('import.mapUndoBlank') }}
                </button>
              </template>
            </div>
          </li>
        </ul>
        <p class="help map-key-help">{{ $t('import.mapKeyHelp') }}</p>
        <details v-if="standard.length" class="map-std-help" data-cy="map-std-help">
          <summary>{{ $t('import.mapStdHelpTitle', { count: standard.length }) }}</summary>
          <p>{{ $t('import.mapStdHelpIntro') }}</p>
          <div class="map-std-help-scroll">
            <table class="table is-narrow">
              <thead>
                <tr>
                  <th>{{ $t('import.mapStdHelpKey') }}</th><th>{{ $t('import.mapStdHelpMeaning') }}</th>
                  <th>{{ $t('import.mapStdHelpFormat') }}</th><th>{{ $t('import.mapStdHelpReads') }}</th>
                  <th>{{ $t('import.mapStdHelpColumns') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="entry in standard" :key="entry.key">
                  <td><code>{{ entry.key }}</code></td>
                  <td>{{ standardLabel(entry.key) }}</td>
                  <td>{{ $t(`import.mapStdFormat.${entry.kind}`) }}</td>
                  <td>{{ $t(`import.mapStdReads.${entry.kind}`) }}</td>
                  <td>{{ entry.aliases.join(', ') }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </details>
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
        <div v-if="valueLines.length" class="map-values" data-cy="map-values">
          <h4 class="title is-6">{{ $t('import.mapValues') }}</h4>
          <div v-for="line in valueLines" :key="line.index" class="map-values-line" :data-cy="`map-values-${line.key}`">
            <p><strong>{{ line.key }}</strong> <span class="map-values-label">{{ line.label }}</span> {{ line.counts }}</p>
            <p v-if="line.conversions" class="map-values-detail">{{ $t('import.mapValuesConversions') }} {{ line.conversions }}</p>
            <p v-if="line.examples" class="map-values-detail is-unreadable">
              {{ $t(line.blank ? 'import.mapValuesBlanked' : 'import.mapValuesUnread') }} {{ line.examples }}
            </p>
          </div>
          <p class="help">{{ $t('import.mapValuesHelp') }}</p>
        </div>
      </section>
      <section v-if="preview.length" class="map-preview" data-cy="map-preview">
        <h3 class="title is-6">{{ $t('import.mapPreview') }}</h3>
        <p class="help">{{ $t('import.mapPreviewHelp') }}</p>
        <div class="map-preview-scroll" tabindex="0" :aria-label="$t('import.mapPreview')">
          <table class="table is-fullwidth">
            <thead>
              <tr>
                <th class="map-preview-row">{{ $t('import.mapPreviewRow') }}</th><th>{{ $t('import.mapEmail') }}</th>
                <th>{{ $t('import.mapName') }}</th><th>{{ $t('import.mapAttribute') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in preview" :key="row.row">
                <td class="map-preview-row" :data-label="$t('import.mapPreviewRow')">{{ row.row }}</td>
                <td class="map-preview-email">{{ row.email }}</td>
                <td>{{ row.name || '—' }}</td>
                <td>
                  <span v-for="[key, value] in row.attributes" :key="key" class="map-attr">
                    <b>{{ key }}</b> {{ Array.isArray(value) ? value.join(', ') : value }}
                    <i v-if="row.changed && row.changed[key] !== undefined" class="map-attr-from">← {{ row.changed[key] }}</i>
                  </span>
                  <span v-for="[key, value] in row.dropped || []" :key="`dropped-${key}`" class="map-attr is-dropped">
                    <b>{{ key }}</b> <s>{{ value }}</s> {{ $t(isBlanked(key) ? 'import.mapPreviewDropped' : 'import.mapPreviewUnread') }}
                  </span>
                  <template v-if="!row.attributes.length && !(row.dropped || []).length">—</template>
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
import { suggestKey, isSpelledKey, standardTwin } from '../lib/csv-import';
import { convertValue, standardByKey, withStandardFirst } from '../lib/standard-attribs';

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
    sourceColumns: { type: Array, default: () => [] },
    samples: { type: Array, default: () => [] },
    columnErrors: { type: Object, default: () => ({}) },
    columnWarnings: { type: Object, default: () => ({}) },
    mapping: { type: Array, default: () => [] },
    existingKeys: { type: Array, default: () => [] },
    // The standard attribute dictionary [{ key, kind, aliases }] and, per column saved under
    // one of its items, how the values were converted ({ index, key, kind, kept, changed, … }).
    standard: { type: Array, default: () => [] },
    values: { type: Array, default: () => [] },
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
        const { role, key, blank } = this.mapping[index];
        const named = ['attribute', 'tags'].includes(role);
        const entry = role === 'attribute' ? standardByKey(this.standard, key) : null;
        const stats = entry && this.values.find((v) => v.index === index && v.key === key);
        const twinKey = role === 'attribute' && !entry && !this.columnErrors[index] ? standardTwin(key, this.standard) : '';
        let state = 'ok';
        let text = this.$t(role === 'tags' ? 'import.mapTagsHelp' : 'import.mapStateOk');
        let fix = '';
        let unreadable = null;
        if (this.columnErrors[index]) {
          state = 'error';
          text = this.columnErrors[index];
          // Offer a ready-made name instead of explaining the naming rule.
          if (named) fix = suggestKey(key || this.sourceColumns[index] || '', this.standard) || `column_${index + 1}`;
          if (this.mapping.some((other) => ['attribute', 'tags'].includes(other.role) && other.key === fix)) fix = '';
        } else if (role === 'discard') {
          state = 'skipped';
          text = this.$t('import.mapStateSkipped');
        } else if (stats && stats.unreadable) {
          // Values that do not fit the standard item are never stored under it as they are.
          state = blank ? 'warning' : 'error';
          text = this.$t(`import.${blank ? 'mapStateBlanked' : 'mapUnreadable'}.${stats.kind}`, { count: this.$utils.formatNumber(stats.unreadable) });
          unreadable = {
            blank: !!blank,
            kind: stats.kind,
            examples: `${stats.examples.map(({ value }) => value).join(', ')}${stats.unreadable > stats.examples.length ? ' …' : ''}`,
          };
        } else if (this.columnWarnings[index]) {
          state = 'warning';
          text = this.columnWarnings[index];
        } else if (stats && stats.changed) {
          text = this.$t(`import.mapStateConverted.${stats.kind}`, { count: this.$utils.formatNumber(stats.changed) });
        } else if (role === 'attribute' && !this.existingKeys.includes(key) && isSpelledKey(this.sourceColumns[index] || '', key)) {
          text = this.$t('import.mapStateAuto');
        }
        const samples = (this.samples[index] || []).map((sample) => sample.trim()).filter(Boolean).map((sample) => {
          const result = entry && state !== 'skipped' ? convertValue(entry.kind, sample) : { status: 'kept' };
          return {
            text: sample, to: result.status === 'changed' ? String(result.value) : '', unreadable: result.status === 'unreadable',
          };
        });
        return {
          index,
          label,
          state,
          text,
          fix,
          samples,
          unreadable,
          standard: entry && { kind: entry.kind, label: this.standardLabel(entry.key) },
          // The standard item this key seems to mean, unless another column already has it.
          twin: twinKey && !this.mapping.some((other) => other.role === 'attribute' && other.key === twinKey)
            ? { key: twinKey, label: this.standardLabel(twinKey) } : null,
        };
      });
    },
    // One line per converted column for the review: counts, examples of changes, unread values.
    valueLines() {
      const n = (count) => this.$utils.formatNumber(count);
      return this.values.filter(({ index, key }) => this.mapping[index] && this.mapping[index].key === key)
        .map((v) => ({
          index: v.index,
          key: v.key,
          label: this.standardLabel(v.key),
          blank: !!this.mapping[v.index].blank,
          counts: [
            v.changed ? this.$t(`import.mapValuesChanged.${v.kind}`, { count: n(v.changed) }) : '',
            v.kept ? this.$t('import.mapValuesKept', { count: n(v.kept) }) : '',
            v.blank ? this.$t('import.mapValuesBlank', { count: n(v.blank) }) : '',
            v.unreadable ? this.$t('import.mapValuesUnreadable', { count: n(v.unreadable) }) : '',
          ].filter(Boolean).join(' · '),
          conversions: v.conversions.map(({ from, to }) => `${from} → ${to}`).join(', '),
          examples: v.examples.map(({ row, value }) => this.$t('import.mapValuesExample', { row, value })).join(', '),
        }));
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
      (row.querySelector(`#map-key-${index}`) || row.querySelector(`#map-role-${index}`)).focus({ preventScroll: true });
    },
    change(index, field, value) {
      this.$emit('mapping', this.mapping.map((entry, n) => {
        if (n !== index) return entry;
        // Leaving unreadable values out was agreed for one item; a new name asks again.
        const { blank, ...rest } = entry;
        return field === 'blank' ? { ...rest, ...(value ? { blank: true } : {}) } : { ...rest, [field]: value };
      }));
    },
    // The i18n label of a standard key ('' for any other key).
    standardLabel(key) {
      return standardByKey(this.standard, key) ? this.$t(`subscribers.standard.${key}`) : '';
    },
    isBlanked(key) {
      return this.mapping.some((entry) => entry.role === 'attribute' && entry.key === key && entry.blank);
    },
    // Puts the cursor in the name box with the name selected, ready to be typed over.
    renameKey(index) {
      const input = this.$el.querySelector(`#map-key-${index}`);
      input.focus();
      input.select();
    },
    selectKey(index, key) {
      // Deselecting an old suggestion must not erase the new text being typed.
      if (typeof key === 'string') this.change(index, 'key', key);
    },
    // Standard items first, then the names already in use.
    keyOptions(query) {
      return withStandardFirst(this.standard, this.existingKeys).filter((key) => key.toLowerCase().includes(query.toLowerCase()));
    },
    issueExamples(kind) {
      return (this.examples[kind] || []).map(({ row, email }) => `${row}${email ? ` (${email})` : ''}`).join(', ');
    },
  },
};
</script>
