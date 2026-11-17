<template>
  <form @submit.prevent="onSubmit">
    <div class="modal-card content" style="width: auto" data-cy="preset-form">
      <header class="modal-card-head">
        <h4>{{ title }}</h4>
      </header>
      <section class="modal-card-body">
        <b-field :label="$t('globals.fields.name')" label-position="on-border"
          :type="nameError ? 'is-danger' : ''" :message="nameError || $t('presets.nameHelp')">
          <b-input v-model="form.name" :maxlength="40" ref="focus" required data-cy="preset-name-input"
            :placeholder="$t('globals.fields.name')" @input="error = ''" />
        </b-field>

        <div class="field">
          <p class="label">
            {{ $t('presets.columns') }}
            <span class="has-text-grey has-text-weight-normal" data-cy="preset-column-count">
              {{ form.columns.length }}/{{ max }}
            </span>
          </p>
          <ol class="preset-edit-list" data-cy="preset-edit-list">
            <li v-for="(key, i) in form.columns" :key="key" data-cy="preset-edit-item">
              <span class="preset-order">{{ i + 1 }}</span>
              <span class="preset-key">{{ key }}</span>
              <span v-if="standardKeys.has(key)" class="preset-std">{{ $t('presets.standard') }}</span>
              <span class="preset-buttons">
                <button type="button" :disabled="i === 0" :aria-label="$t('presets.moveUp', { key })"
                  :title="$t('presets.moveUp', { key })" data-cy="preset-up" @click="move(i, -1)">
                  <b-icon icon="arrow-up" size="is-small" />
                </button>
                <button type="button" :disabled="i === form.columns.length - 1" :aria-label="$t('presets.moveDown', { key })"
                  :title="$t('presets.moveDown', { key })" data-cy="preset-down" @click="move(i, 1)">
                  <b-icon icon="arrow-down" size="is-small" />
                </button>
                <button type="button" :aria-label="$t('presets.remove', { key })" :title="$t('presets.remove', { key })"
                  data-cy="preset-remove" @click="remove(i)">
                  <svg class="preset-svg" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M6 6l12 12M18 6L6 18" /></svg>
                </button>
              </span>
            </li>
          </ol>
          <p v-if="!form.columns.length" class="help preset-none" data-cy="preset-none">{{ $t('presets.noColumns') }}</p>

          <b-field class="preset-add" :type="columnError ? 'is-danger' : ''" :message="columnError">
            <b-autocomplete v-model="newKey" :data="options" maxlength="64" :open-on-focus="true" keep-first
              :disabled="form.columns.length >= max" :placeholder="$t('presets.addPlaceholder')" expanded
              data-cy="preset-add-input" @select="(o) => o && add(o)" @keydown.native.enter.prevent="add(newKey)">
              <template #default="{ option }">
                <span class="preset-option">
                  {{ option }}
                  <span v-if="standardKeys.has(option)" class="preset-std">{{ $t('presets.standard') }}</span>
                </span>
              </template>
              <template #empty>{{ $t('presets.addOwn') }}</template>
            </b-autocomplete>
            <p class="control">
              <b-button :disabled="!newKey.trim() || form.columns.length >= max" data-cy="preset-add" @click="add(newKey)">
                {{ $t('presets.add') }}
              </b-button>
            </p>
          </b-field>
          <p class="help">{{ $t('presets.columnsHelp', { max }) }}</p>
        </div>

        <p v-if="isEditing && data.listCount" class="notification is-info is-light" data-cy="preset-edit-note">
          {{ $t('presets.editNote', { num: data.listCount }) }}
        </p>
        <p v-if="error" class="notification is-danger is-light" role="alert" data-cy="preset-error">{{ error }}</p>
      </section>
      <footer class="modal-card-foot has-text-right">
        <b-button @click="$parent.close()">{{ $t('globals.buttons.close') }}</b-button>
        <b-button native-type="submit" type="is-primary" :loading="saving" data-cy="btn-save">
          {{ isEditing ? $t('globals.buttons.save') : $t('presets.create') }}
        </b-button>
      </footer>
    </div>
  </form>
</template>

<script>
import Vue from 'vue';
import { mapState } from 'vuex';
import {
  PRESET_COLUMNS_MAX, moveItem, presetLabel, validatePresetForm,
} from '../subscriber-columns';

export default Vue.extend({
  name: 'ColumnPresetForm',

  props: {
    // The preset to edit; with isClone, the preset to copy; null for a new one.
    data: { type: Object, default: null },
    presets: { type: Array, default: () => [] },
    isClone: { type: Boolean, default: false },
  },

  data() {
    const src = this.data;
    const name = src && this.isClone ? this.$t('presets.cloneName', { name: presetLabel(src, this.$t.bind(this)) })
      : (src && src.name) || '';
    return {
      form: { name, columns: src ? [...src.columns] : [] },
      newKey: '',
      error: '',
      nameError: '',
      columnError: '',
      saving: false,
      max: PRESET_COLUMNS_MAX,
    };
  },

  computed: {
    ...mapState(['serverConfig']),

    isEditing() { return !!this.data && !this.isClone; },

    title() {
      if (this.isEditing) return this.$t('presets.editTitle', { name: this.data.name });
      return this.$t('presets.newTitle');
    },

    standardKeys() {
      return new Set((this.serverConfig.axm_standard_attribs || []).map(({ key }) => key));
    },

    // Standard keys not chosen yet that match what is typed.
    options() {
      const typed = this.newKey.trim().toLowerCase();
      return [...this.standardKeys].filter((k) => !this.form.columns.includes(k)
        && (!typed || k.toLowerCase().includes(typed)));
    },
  },

  methods: {
    move(i, delta) { this.form.columns = moveItem(this.form.columns, i, delta); },

    remove(i) { this.form.columns = this.form.columns.filter((_, j) => j !== i); },

    add(raw) {
      const key = String(raw || '').trim();
      if (!key) return;
      this.columnError = '';
      if (this.form.columns.includes(key)) {
        this.columnError = this.$t('presets.duplicateColumn');
        return;
      }
      if ([...key].length > 64) {
        this.columnError = this.$t('presets.invalidColumn');
        return;
      }
      if (this.form.columns.length >= this.max) return;
      this.form.columns = [...this.form.columns, key];
      this.$nextTick(() => { this.newKey = ''; });
    },

    onSubmit() {
      this.error = '';
      this.nameError = '';
      const selfId = this.isEditing ? this.data.id : '';
      const code = validatePresetForm(this.form, this.presets, selfId, this.$t.bind(this));
      if (code) {
        const msg = this.$t(`presets.${code}`);
        if (code === 'invalidName' || code === 'exists') this.nameError = msg; else this.error = msg;
        return;
      }
      this.saving = true;
      const body = { name: this.form.name.trim(), columns: this.form.columns.map((c) => c.trim()) };
      const req = this.isEditing
        ? this.$api.updateColumnPreset(this.data.id.slice(1), body) : this.$api.createColumnPreset(body);
      req.then((preset) => {
        // The list form and the subscribers page read the presets from the server config.
        this.$api.getServerConfig();
        this.$emit('finished');
        this.$parent.close();
        this.$utils.toast(this.$t(this.isEditing ? 'globals.messages.updated' : 'globals.messages.created', { name: preset.name }));
      }).finally(() => { this.saving = false; });
    },
  },

  mounted() {
    this.$nextTick(() => { if (this.$refs.focus) this.$refs.focus.focus(); });
  },
});
</script>

<style scoped>
.preset-edit-list { list-style: none; margin: 0 0 8px; padding: 0; border: 1px solid #dce3e8; border-radius: 5px; }
.preset-edit-list li { display: flex; align-items: center; gap: 8px; padding: 6px 8px; border-bottom: 1px solid #eef1f3; }
.preset-edit-list li:last-child { border-bottom: 0; }
.preset-order { min-width: 1.4em; color: #888; font-size: .8rem; text-align: center; }
.preset-key { flex: 1; min-width: 0; overflow-wrap: anywhere; font-family: monospace; }
.preset-std, .preset-option .preset-std { border: 1px solid #b9d3f0; border-radius: 3px; background: #f2f7fd; color: #1d5a9e; padding: 0 5px; font-size: .7rem; white-space: nowrap; }
.preset-buttons { display: flex; gap: 2px; }
.preset-buttons button { border: 0; background: none; cursor: pointer; padding: 6px; border-radius: 4px; color: #555; min-width: 32px; min-height: 32px; }
.preset-buttons button:hover:not(:disabled), .preset-buttons button:focus-visible { background: #eef1f3; color: #0055d4; }
.preset-buttons button:disabled { opacity: .3; cursor: default; }
.preset-svg { width: 1em; height: 1em; fill: none; stroke: currentColor; stroke-width: 2; stroke-linecap: round; vertical-align: -0.12em; }
.preset-none { margin-bottom: 8px; }
.preset-option { display: flex; justify-content: space-between; gap: 10px; align-items: center; }
</style>
