<template>
  <section class="audit">
    <header class="columns page-header">
      <div class="column is-10">
        <h1 class="title is-4 mb-2">
          {{ $t('audit.title') }}
          <span v-if="!loading.audit">({{ page.total }})</span>
        </h1>
        <p class="is-size-7 has-text-grey" data-cy="audit-help">
          {{ $t('audit.help', { months: 12 }) }}
        </p>
      </div>
    </header>

    <form class="audit-filters" @submit.prevent="onFilter">
      <div class="columns is-multiline">
        <div class="column is-3">
          <b-field :label="$t('audit.filter.from')" label-position="on-border">
            <b-input v-model="filter.from" type="date" data-cy="audit-from" />
          </b-field>
        </div>
        <div class="column is-3">
          <b-field :label="$t('audit.filter.to')" label-position="on-border">
            <b-input v-model="filter.to" type="date" data-cy="audit-to" />
          </b-field>
        </div>
        <div class="column is-3">
          <b-field :label="$t('audit.filter.user')" label-position="on-border">
            <b-select v-model="filter.userId" expanded data-cy="audit-user" @input="onFilter">
              <option value="">{{ $t('audit.filter.all') }}</option>
              <option v-for="u in users" :key="u.user_id" :value="u.user_id">{{ u.user_name || u.username }} ({{ u.username }})</option>
            </b-select>
          </b-field>
        </div>
        <div class="column is-3">
          <b-field :label="$t('audit.filter.area')" label-position="on-border">
            <b-select v-model="filter.area" expanded data-cy="audit-area" @input="onArea">
              <option value="">{{ $t('audit.filter.all') }}</option>
              <option v-for="a in areas" :key="a" :value="a">{{ $t(`audit.area.${a}`) }}</option>
            </b-select>
          </b-field>
        </div>
        <div class="column is-3">
          <b-field :label="$t('audit.filter.action')" label-position="on-border">
            <b-select v-model="filter.action" expanded data-cy="audit-action" @input="onFilter">
              <option value="">{{ $t('audit.filter.all') }}</option>
              <option v-for="a in actionChoices" :key="a" :value="a">{{ $t(`audit.action.${a}`) }}</option>
            </b-select>
          </b-field>
        </div>
        <div class="column is-6">
          <b-field>
            <b-input v-model="filter.query" icon="magnify" expanded data-cy="audit-query"
              :placeholder="$t('audit.filter.query')" />
            <p class="controls">
              <b-button native-type="submit" type="is-primary" icon-left="magnify" data-cy="btn-audit-query" />
            </p>
          </b-field>
        </div>
        <div class="column is-3 has-text-right">
          <a href="#" class="is-size-7" data-cy="audit-reset" @click.prevent="onReset">{{ $t('audit.filter.reset') }}</a>
        </div>
      </div>
    </form>

    <b-table :data="page.results" :loading="loading.audit" hoverable detailed detail-key="id" custom-row-key="id"
      :show-detail-icon="true" paginated backend-pagination pagination-position="both" :current-page="page.page"
      :per-page="page.per_page" :total="page.total" @page-change="onPageChange">
      <b-table-column v-slot="props" field="created_at" :label="$t('audit.col.time')" width="14%">
        <span data-cy="audit-time">{{ $utils.niceDate(props.row.created_at, true) }}</span>
      </b-table-column>

      <b-table-column v-slot="props" field="user" :label="$t('audit.col.who')" width="16%">
        <span v-if="props.row.user_type === 'system'" class="has-text-grey">{{ $t('audit.system') }}</span>
        <template v-else>
          <span data-cy="audit-who">{{ props.row.user_name || props.row.username }}</span>
          <div class="is-size-7 has-text-grey">
            {{ props.row.username }}
            <b-tag v-if="props.row.user_type === 'api'" class="is-small">API</b-tag>
          </div>
        </template>
      </b-table-column>

      <b-table-column v-slot="props" field="action" :label="$t('audit.col.action')" width="18%">
        <b-tag :class="`audit-${areaOf(props.row.action)}`" data-cy="audit-action-label">
          {{ actionLabel(props.row.action) }}
        </b-tag>
      </b-table-column>

      <b-table-column v-slot="props" field="target" :label="$t('audit.col.target')">
        <router-link v-if="targetLink(props.row)" :to="targetLink(props.row)" data-cy="audit-target">
          {{ targetText(props.row) }}
        </router-link>
        <span v-else data-cy="audit-target">{{ targetText(props.row) }}</span>
      </b-table-column>

      <b-table-column v-slot="props" field="count" :label="$t('audit.col.count')" numeric width="8%">
        <span data-cy="audit-count">{{ props.row.count === null ? '' : $utils.formatNumber(props.row.count) }}</span>
      </b-table-column>

      <template #detail="props">
        <dl class="audit-detail" data-cy="audit-detail">
          <template v-for="(l, i) in lines(props.row)">
            <dt :key="`k${i}`">{{ l.label }}</dt>
            <dd :key="`v${i}`">{{ l.text }}</dd>
          </template>
          <template v-if="!lines(props.row).length">
            <dt>&nbsp;</dt>
            <dd class="has-text-grey">{{ $t('audit.noDetail') }}</dd>
          </template>
        </dl>
      </template>

      <template #empty v-if="!loading.audit">
        <empty-placeholder />
      </template>
    </b-table>
  </section>
</template>

<script>
import Vue from 'vue';
import { mapState } from 'vuex';
import EmptyPlaceholder from '../components/EmptyPlaceholder.vue';
import { AUDIT_ACTIONS, AUDIT_AREAS, detailLines } from '../audit';

const emptyFilter = () => ({
  from: '', to: '', userId: '', area: '', action: '', query: '',
});

export default Vue.extend({
  components: { EmptyPlaceholder },

  data() {
    return {
      page: {
        results: [], total: 0, page: 1, per_page: 20,
      },
      users: [],
      filter: emptyFilter(),
      areas: AUDIT_AREAS,
    };
  },

  computed: {
    ...mapState(['loading']),

    actionChoices() {
      return this.filter.area ? AUDIT_ACTIONS[this.filter.area] : Object.values(AUDIT_ACTIONS).flat();
    },
  },

  methods: {
    areaOf(action) { return (action || '').split('.')[0]; },

    actionLabel(action) {
      return this.$te(`audit.action.${action}`) ? this.$t(`audit.action.${action}`) : action;
    },

    targetText(row) {
      if (row.target_type === 'subscriber') {
        if (row.target_id) return `${this.$t('audit.target.subscriber')} #${row.target_id}`;
        return this.$t('audit.target.subscribers');
      }
      if (row.target_type === 'import') return this.$t('audit.target.import');
      if (row.target_type === 'settings') return this.$t('audit.target.settings');
      return row.target_name || (row.target_id ? `#${row.target_id}` : '');
    },

    // Lists and campaigns link to their screens (a deleted one opens an empty form, so only
    // the actions that leave them standing link).
    targetLink(row) {
      if (!row.target_id) return null;
      if (row.target_type === 'campaign' && row.action !== 'campaign.delete') {
        return { name: 'campaign', params: { id: row.target_id } };
      }
      if (row.target_type === 'list' && !['list.delete', 'list.purge'].includes(row.action)) {
        return { name: 'list', params: { id: row.target_id } };
      }
      if (row.target_type === 'list') return { name: 'listTrash' };
      return null;
    },

    lines(row) { return detailLines(row.detail, this.$t.bind(this), this.$te.bind(this)); },

    params() {
      const p = { page: this.page.page, per_page: this.page.per_page };
      const f = this.filter;
      // A date is a day of the viewer: from its start to the start of the day after "to".
      if (f.from) p.from = new Date(`${f.from}T00:00:00`).toISOString();
      if (f.to) {
        const d = new Date(`${f.to}T00:00:00`);
        d.setDate(d.getDate() + 1);
        p.to = d.toISOString();
      }
      if (f.userId) p.user_id = f.userId;
      if (f.area) p.area = f.area;
      if (f.action) p.action = f.action;
      if (f.query.trim()) p.query = f.query.trim();
      return p;
    },

    load() {
      this.$api.getAudit(this.params()).then((resp) => {
        this.page = resp;
      });
    },

    onFilter() {
      this.page.page = 1;
      this.load();
    },

    onArea() {
      if (this.filter.action && !this.actionChoices.includes(this.filter.action)) this.filter.action = '';
      this.onFilter();
    },

    onReset() {
      this.filter = emptyFilter();
      this.onFilter();
    },

    onPageChange(p) {
      this.page.page = p;
      this.load();
    },
  },

  mounted() {
    this.load();
    this.$api.getAuditUsers().then((u) => { this.users = u; });
  },
});
</script>
