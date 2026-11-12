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

    this.$nextTick(() => {
      this.$refs.focus.focus();
    });
  },
});
</script>

<style scoped>
.dynamic-list-form { width: 100% !important; max-width: 100%; margin: 0; }
@media screen and (max-width: 600px) {
  .modal-card-body { padding: 20px 14px; }
}
</style>
