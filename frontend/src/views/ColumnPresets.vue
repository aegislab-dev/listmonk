<template>
  <section class="column-presets">
    <header class="columns page-header">
      <div class="column is-10">
        <h1 class="title is-4 mb-2">
          {{ $t('presets.title') }}
          <span v-if="!loading.columnPresets">({{ presets.length }})</span>
        </h1>
        <p class="is-size-7 has-text-grey" data-cy="presets-help">{{ $t('presets.help') }}</p>
        <p v-if="!canManage" class="is-size-7 has-text-grey" data-cy="presets-readonly">{{ $t('presets.readOnly') }}</p>
      </div>
      <div class="column has-text-right">
        <b-field v-if="canManage" expanded>
          <b-button expanded type="is-primary" icon-left="plus" class="btn-new" data-cy="btn-new" @click="showNewForm">
            {{ $t('globals.buttons.new') }}
          </b-button>
        </b-field>
      </div>
    </header>

    <b-table :data="presets" :loading="loading.columnPresets" hoverable class="presets-table">
      <b-table-column v-slot="props" field="name" :label="$t('globals.fields.name')" cell-class="preset-name-cell">
        <a v-if="canManage && !props.row.builtin" href="#" data-cy="preset-name" @click.prevent="showEditForm(props.row)">
          {{ label(props.row) }}
        </a>
        <span v-else data-cy="preset-name">{{ label(props.row) }}</span>
        <b-tag v-if="props.row.builtin" class="is-small preset-builtin" data-cy="preset-builtin">{{ $t('presets.builtin') }}</b-tag>
      </b-table-column>

      <b-table-column v-slot="props" field="columns" :label="$t('presets.columns')">
        <ol class="preset-columns" data-cy="preset-columns">
          <li v-for="(key, i) in props.row.columns" :key="key">
            <span class="preset-order">{{ i + 1 }}</span>{{ key }}
          </li>
        </ol>
      </b-table-column>

      <b-table-column v-slot="props" field="listCount" :label="$t('presets.usedBy')" cell-class="preset-count-cell">
        <span data-cy="preset-count">{{ $t('presets.numLists', { num: props.row.listCount }) }}</span>
      </b-table-column>

      <b-table-column v-slot="props" cell-class="actions" align="right">
        <div v-if="canManage">
          <a href="#" data-cy="btn-clone" :aria-label="$t('presets.clone')" @click.prevent="showCloneForm(props.row)">
            <b-tooltip :label="$t('presets.clone')" type="is-dark">
              <svg class="preset-svg" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15V6a2 2 0 0 1 2-2h8" /></svg>
            </b-tooltip>
          </a>
          <template v-if="!props.row.builtin">
            <a href="#" data-cy="btn-edit" :aria-label="$t('globals.buttons.edit')" @click.prevent="showEditForm(props.row)">
              <b-tooltip :label="$t('globals.buttons.edit')" type="is-dark">
                <b-icon icon="pencil-outline" size="is-small" />
              </b-tooltip>
            </a>
            <a href="#" data-cy="btn-delete" :aria-label="$t('globals.buttons.delete')" @click.prevent="onDelete(props.row)">
              <b-tooltip :label="$t('globals.buttons.delete')" type="is-dark">
                <b-icon icon="trash-can-outline" size="is-small" />
              </b-tooltip>
            </a>
          </template>
        </div>
      </b-table-column>

      <template #empty v-if="!loading.columnPresets">
        <empty-placeholder />
      </template>
    </b-table>

    <b-modal scroll="keep" :aria-modal="true" :active.sync="isFormVisible" :width="640">
      <column-preset-form :data="curPreset" :presets="presets" :is-clone="isClone" @finished="load" />
    </b-modal>
  </section>
</template>

<script>
import Vue from 'vue';
import { mapState } from 'vuex';
import ColumnPresetForm from '../components/ColumnPresetForm.vue';
import EmptyPlaceholder from '../components/EmptyPlaceholder.vue';
import { presetLabel } from '../subscriber-columns';

export default Vue.extend({
  components: { ColumnPresetForm, EmptyPlaceholder },

  data() {
    return {
      presets: [],
      curPreset: null,
      isClone: false,
      isFormVisible: false,
    };
  },

  computed: {
    ...mapState(['loading', 'serverConfig']),

    canManage() { return this.$can('lists:manage_all'); },

    serverDefault() { return this.serverConfig.axm_default_column_preset; },
  },

  methods: {
    label(preset) {
      return presetLabel(preset, this.$t.bind(this));
    },

    load() {
      this.$api.getColumnPresets().then((presets) => { this.presets = presets; });
    },

    showNewForm() {
      this.curPreset = null;
      this.isClone = false;
      this.isFormVisible = true;
    },

    showEditForm(preset) {
      if (!this.canManage || preset.builtin) return;
      this.curPreset = preset;
      this.isClone = false;
      this.isFormVisible = true;
    },

    // A copy of a preset, built in or not, as the start of a new one.
    showCloneForm(preset) {
      this.curPreset = preset;
      this.isClone = true;
      this.isFormVisible = true;
    },

    onDelete(preset) {
      const name = this.label(preset);
      const done = () => {
        this.$api.deleteColumnPreset(preset.id.slice(1)).then(() => {
          this.$utils.toast(this.$t('globals.messages.deleted', { name }));
          this.load();
          this.$api.getServerConfig();
        });
      };
      if (!preset.listCount) {
        this.$utils.confirm(this.$t('presets.confirmDelete', { name }), done);
        return;
      }
      const names = (preset.lists || []).map((l) => l.name).join(', ');
      const more = preset.listCount - (preset.lists || []).length;
      const listText = more > 0 ? this.$t('presets.andMore', { names, num: more }) : names;
      const fallback = this.$t(`lists.preset.${this.serverDefault}`);
      this.$utils.confirmWarn(
        this.$t('presets.confirmDeleteTitle', { name }),
        this.$t('presets.confirmDeleteLists', { num: preset.listCount, lists: listText, preset: fallback }),
        this.$t('globals.buttons.delete'),
        done,
      );
    },
  },

  created() {
    this.$root.$on('page.refresh', this.load);
  },

  destroyed() {
    this.$root.$off('page.refresh', this.load);
  },

  mounted() {
    this.load();
  },
});
</script>

<style scoped>
.preset-builtin { margin-left: 6px; }
.preset-columns { list-style: none; margin: 0; padding: 0; display: flex; flex-wrap: wrap; gap: 4px 6px; }
.preset-columns li { background: #f2f4f6; border-radius: 4px; padding: 1px 8px 1px 4px; font-size: .85rem; white-space: nowrap; }
.preset-order { display: inline-block; min-width: 1.2em; margin-right: 4px; color: #888; font-size: .75rem; text-align: center; }
.preset-count-cell { white-space: nowrap; }
.preset-svg { width: 1.05em; height: 1.05em; vertical-align: -0.15em; fill: none; stroke: currentColor; stroke-width: 1.8; stroke-linecap: round; stroke-linejoin: round; }
</style>
