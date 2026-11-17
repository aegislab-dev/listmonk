<template>
  <section class="list-trash">
    <header class="columns page-header">
      <div class="column is-10">
        <h1 class="title is-4 mb-2">
          <router-link :to="{ name: 'lists' }" class="has-text-grey-light" data-cy="back-to-lists">
            {{ $t('globals.terms.lists') }}
          </router-link>
          <span class="has-text-grey-light"> / </span>{{ $t('lists.trash.title') }}
          <span v-if="!loading.lists">({{ results.length }})</span>
        </h1>
      </div>
    </header>

    <p class="notification is-info is-light" data-cy="trash-help">
      {{ $t('lists.trash.help', { days: trashDays }) }}
    </p>

    <b-table :data="results" :loading="loading.lists" hoverable>
      <b-table-column v-slot="props" field="name" :label="$t('globals.fields.name')" width="30%">
        <span data-cy="trash-name">{{ props.row.name }}</span>
        <b-taglist>
          <b-tag v-if="props.row.dynamic" type="is-info is-light">{{ $t('lists.dynamic.dynamic') }}</b-tag>
          <b-tag v-else :class="props.row.type">{{ $t(`lists.types.${props.row.type}`) }}</b-tag>
          <b-tag v-for="t in teamTagsOf(props.row)" :key="t" type="is-info is-light" class="team-badge">
            <b-icon icon="account-multiple" size="is-small" /> {{ teamName(t) }}
          </b-tag>
        </b-taglist>
      </b-table-column>

      <b-table-column v-slot="props" field="subscriberCount" :label="$t('globals.terms.subscribers')" numeric>
        {{ $utils.formatNumber(props.row.subscriberCount) }}
      </b-table-column>

      <b-table-column v-slot="props" field="deletedBy" :label="$t('lists.trash.deletedBy')">
        <span data-cy="trash-by">{{ props.row.deletedByName || props.row.deletedByUsername || '–' }}</span>
      </b-table-column>

      <b-table-column v-slot="props" field="deletedAt" :label="$t('lists.trash.deletedAt')">
        {{ $utils.niceDate(props.row.deletedAt, true) }}
      </b-table-column>

      <b-table-column v-slot="props" field="daysLeft" :label="$t('lists.trash.daysLeft')">
        <b-tag :type="props.row.daysLeft <= 3 ? 'is-warning is-light' : 'is-light'" data-cy="trash-days-left">
          {{ $t('lists.trash.numDaysLeft', { num: props.row.daysLeft }) }}
        </b-tag>
      </b-table-column>

      <b-table-column v-slot="props" cell-class="actions" align="right">
        <b-button size="is-small" icon-left="refresh" data-cy="btn-restore" @click="onRestore(props.row)">
          {{ $t('lists.trash.restore') }}
        </b-button>
      </b-table-column>

      <template #empty v-if="!loading.lists">
        <empty-placeholder />
      </template>
    </b-table>
  </section>
</template>

<script>
import Vue from 'vue';
import { mapState } from 'vuex';
import EmptyPlaceholder from '../components/EmptyPlaceholder.vue';
import { teamName, teamTags } from '../teams';

export default Vue.extend({
  components: { EmptyPlaceholder },

  data() {
    return { results: [], trashDays: 14 };
  },

  computed: {
    ...mapState(['loading']),
  },

  methods: {
    teamName,

    teamTagsOf(row) { return teamTags(row.tags || []); },

    load() {
      this.$api.getListTrash().then((resp) => {
        this.results = resp.results || [];
        if (resp.trashDays) this.trashDays = resp.trashDays;
      });
    },

    onRestore(row) {
      this.$utils.confirm(this.$t('lists.trash.confirmRestore', { name: row.name }), () => {
        this.$api.restoreList(row.id).then((resp) => {
          this.load();
          this.$utils.toast(this.$t('lists.trash.restored', { name: row.name, num: resp.subscriptions }));
        });
      });
    },
  },

  mounted() {
    this.load();
  },
});
</script>
