<template>
  <form @submit.prevent="onSubmit">
    <div class="modal-card content dynamic-list-form" style="width: auto">
      <header class="modal-card-head">
        <p v-if="isEditing" class="has-text-grey-light is-size-7">
          {{ $t('globals.fields.id') }}: <copy-text :text="`${data.id}`" />
          {{ $t('globals.fields.uuid') }}: <copy-text :text="data.uuid" />
        </p>
        <b-tag v-if="isEditing" :class="[composition === 'dynamic' ? 'is-info is-light' : data.type, 'is-pulled-right']">
          {{ composition === 'dynamic' ? $t('lists.dynamic.dynamic') : $t(`lists.types.${data.type}`) }}
        </b-tag>
        <h4 v-if="isEditing">
          {{ data.name }}
        </h4>
        <h4 v-else>
          {{ $t('lists.newList') }}
        </h4>
      </header>
      <section expanded class="modal-card-body">
        <div v-if="isEditing && drawnRecord" class="draw-summary" data-cy="draw-summary">
          <p class="label">{{ $t('lists.draw.drawnList') }}</p>
          <table class="draw-summary-table">
            <tbody>
              <tr>
                <td>{{ $t('lists.draw.from') }}</td>
                <td>
                  <a v-if="drawnRecord.sourceListId" href="#" @click.prevent="$emit('open-list', drawnRecord.sourceListId)">{{ drawnRecord.sourceName }}</a>
                  <span v-else>{{ drawnRecord.sourceName }} <span class="has-text-grey">({{ $t('lists.draw.historyDeleted') }})</span></span>
                </td>
              </tr>
              <tr>
                <td>{{ $t('lists.draw.at') }}</td>
                <td>{{ $utils.niceDate(drawnRecord.drawnAt, true) }}{{ drawnRecord.drawnByName ? ` · ${drawnRecord.drawnByName}` : '' }}</td>
              </tr>
              <tr><td>{{ $t('lists.draw.settings') }}</td><td>{{ settingsText }}</td></tr>
              <tr class="sub">
                <td>{{ $t('lists.draw.numbers') }}</td>
                <td>
                  {{ $t('lists.draw.rowTarget') }} {{ $utils.formatNumber(drawnRecord.result.target || 0) }} ·
                  {{ $t('lists.draw.rowDrawn') }} {{ $utils.formatNumber(drawnRecord.result.alreadyDrawn || 0) }} ·
                  {{ $t('lists.draw.rowReceived') }} {{ $utils.formatNumber(drawnRecord.result.alreadyReceived || 0) }} ·
                  {{ $t('lists.draw.rowCandidates') }} {{ $utils.formatNumber(drawnRecord.result.candidates || 0) }}
                </td>
              </tr>
            </tbody>
          </table>
          <p class="help">{{ $t('lists.draw.drawnHelp') }}</p>
        </div>
        <b-field :label="$t('lists.dynamic.composition')">
          <b-select v-model="composition" :disabled="isEditing" expanded data-cy="list-composition">
            <option value="static">{{ $t('lists.dynamic.static') }}</option>
            <option value="dynamic">{{ $t('lists.dynamic.dynamic') }}</option>
          </b-select>
        </b-field>
        <b-field :label="$t('globals.fields.name')" label-position="on-border">
          <b-input :maxlength="200" :ref="'focus'" v-model="form.name" name="name"
            :placeholder="$t('globals.fields.name')" required />
        </b-field>

        <b-field v-if="composition === 'static'" :label="$t('lists.type')" label-position="on-border" :message="$t('lists.typeHelp')">
          <b-select v-model="form.type" name="type" :placeholder="$t('lists.typeHelp')" required expanded>
            <option value="private">
              {{ $t('lists.types.private') }}
            </option>
            <option value="public">
              {{ $t('lists.types.public') }}
            </option>
          </b-select>
        </b-field>

        <b-field v-if="composition === 'static'" :label="$t('lists.optin')" label-position="on-border" :message="$t('lists.optinHelp')">
          <b-select v-model="form.optin" name="optin" placeholder="Opt-in type" required expanded>
            <option value="single">
              {{ $t('lists.optins.single') }}
            </option>
            <option value="double">
              {{ $t('lists.optins.double') }}
            </option>
          </b-select>
        </b-field>

        <dynamic-list-filter v-if="composition === 'dynamic'" v-model="filter" :list-id="data.id || 0" />
        <p v-if="filterError" role="alert" class="help is-danger">{{ filterError }}</p>
        <!-- Shown only when there is a choice to make: with one preset every list has it. -->
        <b-field v-if="presets.length > 1" :label="$t('lists.preset.label')" label-position="on-border"
          :message="presetHelp">
          <b-select v-model="preset" expanded data-cy="list-preset" :disabled="!canSave"
            @input="presetTouched = true">
            <option v-for="p in presets" :key="p.id" :value="p.id">
              {{ $t(`lists.preset.${p.id}`) }} — {{ p.columns.join(', ') }}
            </option>
          </b-select>
        </b-field>
        <b-field :label="$t('globals.terms.tags')" label-position="on-border">
          <b-taginput v-model="form.tags" name="tags" ellipsis icon="tag-outline"
            :placeholder="$t('globals.terms.tags')" />
        </b-field>

        <b-field :label="$t('globals.fields.description')" label-position="on-border">
          <b-input :maxlength="2000" v-model="form.description" name="description" type="textarea"
            :placeholder="$t('globals.fields.description')" />
        </b-field>

        <b-field :message="$t('lists.archivedHelp')" :label="$t('lists.archived')">
          <b-switch v-model="isArchived" name="status" />
        </b-field>

        <!-- Lists drawn from this one (LST-23) -->
        <div v-if="isEditing && sourceDraws.length" class="draw-history" data-cy="draw-history">
          <p class="label">{{ $t('lists.draw.history') }} <span class="has-text-grey is-size-7">({{ sourceDraws.length }})</span></p>
          <div class="draw-history-scroll">
            <table class="table is-narrow is-fullwidth">
              <thead>
                <tr>
                  <th>{{ $t('lists.draw.historyTime') }}</th><th>{{ $t('lists.draw.historyNum') }}</th>
                  <th>{{ $t('lists.draw.historyList') }}</th><th>{{ $t('lists.draw.historyCamps') }}</th><th />
                </tr>
              </thead>
              <tbody>
                <tr v-for="d in sourceDraws" :key="d.id" :data-cy="`draw-row-${d.id}`">
                  <td>{{ $utils.niceDate(d.drawnAt, true) }}</td>
                  <td>{{ $t('lists.draw.numFormat', { num: $utils.formatNumber(d.result.drawn || 0) }) }}</td>
                  <td>
                    <a v-if="d.listId" href="#" @click.prevent="$emit('open-list', d.listId)">{{ d.listName }}</a>
                    <span v-else class="has-text-grey">{{ $t('lists.draw.historyDeleted') }} &middot; {{ d.listName }}</span>
                  </td>
                  <td>{{ d.listId ? campaignsText(d) : '' }}</td>
                  <td class="draw-actions">
                    <template v-if="d.listId && canManageDrawn(d)">
                      <a href="#" :data-cy="`draw-archive-${d.id}`" @click.prevent="archiveDrawn(d)">{{ $t('lists.draw.historyArchive') }}</a>
                      &middot;
                      <a href="#" :data-cy="`draw-delete-${d.id}`" @click.prevent="deleteDrawn(d)">{{ $t('lists.draw.historyDelete') }}</a>
                    </template>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <p class="help">{{ $t('lists.draw.historyHelp') }}</p>
        </div>
        <div v-if="isEditing && $can('lists:manage_all') && !isDrawnList && data.status !== 'archived'" class="draw-history-button">
          <b-button size="is-small" data-cy="btn-draw-from-form" @click="$emit('draw', data)">
            {{ $t('lists.draw.action') }}
          </b-button>
        </div>
      </section>
      <footer class="modal-card-foot has-text-right">
        <b-button @click="$parent.close()">
          {{ $t('globals.buttons.close') }}
        </b-button>
        <b-button v-if="canSave" native-type="submit"
          type="is-primary" :loading="loading.lists" data-cy="btn-save">
          {{ $t('globals.buttons.save') }}
        </b-button>
      </footer>
    </div>
  </form>
</template>

<script>
import Vue from 'vue';
import { mapState } from 'vuex';
import CopyText from '../components/CopyText.vue';
import DynamicListFilter from '../components/DynamicListFilter.vue';
import { filterToForm, formToFilter, validateDynamicForm } from '../dynamic-list';
import { recordSettings } from '../list-draw';

export default Vue.extend({
  name: 'ListForm',

  components: {
    CopyText, DynamicListFilter,
  },

  props: {
    data: { type: Object, default: () => ({}) },
    isEditing: { type: Boolean, default: false },
  },

  data() {
    return {
      // Draw records: the lists drawn from this one, and the record of this list if it was drawn.
      sourceDraws: [],
      drawnRecord: null,
      composition: this.data.axmFilter ? 'dynamic' : 'static',
      filter: filterToForm(this.data.axmFilter),
      filterError: '',
      // The column preset of the subscribers table. A new dynamic list follows its base
      // list's preset until the user picks one.
      preset: this.data.axmPreset || '',
      presetTouched: false,
      // Binds form input values.
      form: {
        name: '',
        type: 'private',
        optin: 'single',
        status: 'active',
        tags: [],
      },
    };
  },

  methods: {
    loadDraws() {
      if (!this.isEditing || !this.data.id) return;
      this.$api.getListDraws({ source_id: this.data.id }).then((rows) => { this.sourceDraws = rows || []; }).catch(() => {});
      this.$api.getListDraws({ list_id: [this.data.id] }).then((rows) => { this.drawnRecord = (rows || [])[0] || null; }).catch(() => {});
    },

    campaignsText(d) {
      if (!d.campaigns) return '0';
      return d.campaignsFinished ? this.$t('lists.draw.campaignsDone', { num: d.campaigns, done: d.campaignsFinished }) : String(d.campaigns);
    },

    // Archiving or deleting a drawn list is the ordinary list API; $canManageList covers permission.
    canManageDrawn(d) { return this.$can('lists:manage_all') || this.$canManageList(d.listId); },

    archiveDrawn(d) {
      this.$api.getList(d.listId).then((l) => this.$api.updateList({
        id: l.id, name: l.name, type: l.type, optin: l.optin, status: 'archived', tags: l.tags, description: l.description, axm_preset: l.axmPreset,
      })).then(() => {
        this.$utils.toast(this.$t('globals.messages.updated', { name: d.listName }));
        this.$emit('finished');
        this.loadDraws();
      });
    },

    deleteDrawn(d) {
      this.$utils.confirm(this.$t('lists.draw.confirmDelete', { name: d.listName }), () => {
        this.$api.deleteList(d.listId).then(() => {
          this.$utils.toast(this.$t('globals.messages.deleted', { name: d.listName }));
          this.$emit('finished');
          this.loadDraws();
        });
      });
    },

    payload() {
      return {
        ...this.form,
        axm_filter: this.composition === 'dynamic' ? formToFilter(this.filter) : null,
        axm_preset: this.preset,
      };
    },
    onSubmit() {
      this.filterError = '';
      if (this.composition === 'dynamic') {
        const error = validateDynamicForm(this.filter);
        if (error) { this.filterError = this.$t(`lists.dynamic.${error}`); return; }
      }
      if (this.isEditing) {
        this.updateList();
        return;
      }

      this.createList();
    },

    createList() {
      this.$api.createList(this.payload()).then((data) => {
        this.$emit('finished');
        this.$parent.close();
        this.$utils.toast(this.$t('globals.messages.created', { name: data.name }));
      });
    },

    updateList() {
      this.$api.updateList({ id: this.data.id, ...this.payload() }).then((data) => {
        this.$emit('finished');
        this.$parent.close();
        this.$utils.toast(this.$t('globals.messages.updated', { name: data.name }));
      });
    },
  },

  computed: {
    ...mapState(['loading', 'profile', 'lists', 'serverConfig']),

    presets() {
      return this.serverConfig.axm_column_presets || [];
    },

    isDrawnList() { return !!this.drawnRecord; },

    settingsText() {
      const s = recordSettings(this.drawnRecord.settings);
      return this.$t('lists.draw.settingsText', {
        num: s.unit === 'percent' ? `${s.value}%` : this.$t('lists.draw.numFormat', { num: this.$utils.formatNumber(s.value) }),
        scope: this.$t(s.scope === 'all' ? 'lists.draw.scopeAll' : 'lists.draw.scopeSame').replace(/\s*\(.*\)$/, ''),
        period: s.days ? this.$t('lists.draw.periodRecent', { days: s.days }) : this.$t('lists.draw.periodAll').replace(/\s*\(.*\)$/, ''),
      });
    },

    // The server's rule: creating takes lists:manage_all, editing that or list:manage on the list.
    canSave() {
      return this.isEditing ? this.$canManageList(this.data.id) : this.$can('lists:manage_all');
    },

    // The preset a new dynamic list takes over: that of its first base list.
    inheritedPreset() {
      if (this.isEditing || this.composition !== 'dynamic' || this.filter.base.type !== 'lists') return null;
      const base = (this.lists.results || []).find((l) => l.id === (this.filter.base.lists || [])[0]);
      return base ? { id: base.axmPreset, name: base.name } : null;
    },

    presetHelp() {
      const inherited = this.inheritedPreset;
      return inherited && !this.presetTouched && inherited.id === this.preset
        ? this.$t('lists.preset.inherited', { name: inherited.name }) : this.$t('lists.preset.help');
    },

    isArchived: {
      get() {
        return this.form.status === 'archived';
      },
      set(v) {
        this.form.status = v ? 'archived' : 'active';
      },
    },
  },

  watch: {
    // Until the user picks a preset, a new list follows the base list (or the default).
    inheritedPreset: {
      immediate: true,
      handler(inherited) {
        if (this.isEditing || this.presetTouched) return;
        const id = inherited ? inherited.id : this.serverConfig.axm_default_column_preset;
        this.preset = this.presets.some((p) => p.id === id) ? id : this.serverConfig.axm_default_column_preset;
      },
    },
  },

  mounted() {
    this.form = { ...this.form, ...this.$props.data };
    this.loadDraws();

    this.$nextTick(() => {
      this.$refs.focus.focus();
    });
  },
});
</script>

<style scoped>
.dynamic-list-form { width: 100% !important; max-width: 100%; margin: 0; }
.draw-history, .draw-summary { background: #f8fafb; border: 1px solid #dce3e8; border-radius: 5px; padding: 14px 16px; margin: 20px 0 8px; }
.draw-summary { margin: 0 0 20px; }
.draw-history-scroll { overflow-x: auto; }
.draw-history .table { margin: 0; font-size: .85rem; background: transparent; }
.draw-history th, .draw-history td { white-space: nowrap; }
.draw-summary-table { width: 100%; font-size: .9rem; border-collapse: collapse; background: transparent; }
.draw-summary-table td { padding: 4px 0; border: 0; border-bottom: 1px solid #e6ebef; vertical-align: top; }
.draw-summary-table td:first-child { width: 110px; color: #555; }
.draw-summary-table tr.sub td { font-size: .85rem; color: #555; }
.draw-history-button { margin: 12px 0 0; }
@media screen and (max-width: 600px) {
  .modal-card-body { padding: 20px 14px; }
}
</style>
