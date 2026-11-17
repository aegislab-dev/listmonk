<template>
  <section class="lists">
    <header class="columns page-header">
      <div class="column is-10">
        <h1 class="title is-4 mb-2">
          {{ $t('globals.terms.lists') }}
          <span v-if="queryParams.status === 'archived'" class="has-text-grey-light">/ {{ queryParams.status }} </span>
          <span v-if="!isNaN(lists.total)">({{ lists.total }})</span>
          <b-tag v-if="myTeam" type="is-info is-light" class="my-team" data-cy="my-team">
            <b-icon icon="account-multiple" size="is-small" /> {{ myTeamName }}
          </b-tag>
        </h1>

        <div class="is-size-7">
          <router-link v-if="queryParams.status !== 'archived'" :to="{ name: 'lists', query: { status: 'archived' } }">
            {{ $t('globals.buttons.view') }} {{ $t('lists.archived').toLowerCase() }} &rarr;
          </router-link>
          <router-link v-else :to="{ name: 'lists' }">
            {{ $t('globals.buttons.view') }} {{ $t('menu.allLists').toLowerCase() }} &rarr;
          </router-link>
          <template v-if="queryParams.status !== 'archived' && teamless > 0 && queryParams.team !== NO_TEAM">
            &nbsp;&middot;&nbsp;
            <a href="#" data-cy="teamless-link" @click.prevent="setTeam(NO_TEAM)">
              {{ $t('teams.teamlessLink', { num: teamless }) }} &rarr;
            </a>
          </template>
          <template>
            &nbsp;&middot;&nbsp;
            <router-link :to="{ name: 'listTrash' }" data-cy="trash-link">
              <b-icon icon="trash-can-outline" size="is-small" /> {{ $t('lists.trash.link') }} &rarr;
            </router-link>
          </template>
          <template v-if="queryParams.status !== 'archived'">
            &nbsp;&middot;&nbsp;
            <a v-if="!queryParams.drawn" href="#" data-cy="drawn-only" @click.prevent="setDrawn(true)">
              {{ $t('lists.draw.onlyDrawn') }} &rarr;
            </a>
            <a v-else href="#" data-cy="drawn-all" @click.prevent="setDrawn(false)">
              {{ $t('lists.draw.allLists') }} &rarr;
            </a>
          </template>
        </div>
      </div>
      <div class="column has-text-right">
        <b-field v-if="$canCreateList()" expanded>
          <b-button expanded type="is-primary" icon-left="plus" class="btn-new" @click="showNewForm" data-cy="btn-new">
            {{ $t('globals.buttons.new') }}
          </b-button>
        </b-field>
      </div>
    </header>

    <b-table :data="lists.results" :loading="loading.listsFull" @check-all="onTableCheck" @check="onTableCheck"
      :checked-rows.sync="bulk.checked" hoverable default-sort="createdAt" paginated backend-pagination
      pagination-position="both" @page-change="onPageChange" :current-page="queryParams.page" :per-page="lists.perPage"
      :total="lists.total" checkable backend-sorting @sort="onSort">
      <template #top-left>
        <div class="columns">
          <div class="column" :class="showTeamFilter ? 'is-5' : 'is-6'">
            <form @submit.prevent="getLists">
              <b-field>
                <b-input v-model="queryParams.query" name="query" expanded icon="magnify" ref="query" data-cy="query" />
                <p class="controls">
                  <b-button native-type="submit" type="is-primary" icon-left="magnify" data-cy="btn-query" />
                </p>
              </b-field>
            </form>
          </div>
          <div v-if="showTeamFilter" class="column is-3">
            <b-select v-model="queryParams.team" expanded data-cy="team-filter" :aria-label="$t('teams.filter')"
              @input="onTeamFilter">
              <option value="">{{ $t('teams.filterAll') }}</option>
              <option v-for="t in teamOptions" :key="t.tag" :value="t.tag">{{ t.name }} ({{ t.lists }})</option>
              <option v-if="teamless > 0" :value="NO_TEAM">{{ $t('teams.filterNone', { num: teamless }) }}</option>
            </b-select>
          </div>
        </div>
        <div class="actions" v-if="bulk.checked.length > 0">
          <a class="a" href="#" @click.prevent="deleteLists" data-cy="btn-delete-lists">
            <b-icon icon="trash-can-outline" size="is-small" /> {{ $t('globals.buttons.delete') }}
          </a>
          <span class="a">
            {{ $tc('globals.messages.numSelected', numSelectedLists, { num: numSelectedLists }) }}
            <span v-if="!bulk.all && lists.total > lists.perPage">
              &mdash;
              <a href="#" @click.prevent="onSelectAll" data-cy="select-all-lists">
                {{ $tc('globals.messages.selectAll', lists.total, { num: lists.total }) }}
              </a>
            </span>
          </span>
        </div>
      </template>

      <b-table-column v-slot="props" field="name" :label="$t('globals.fields.name')" header-class="cy-name" sortable
        width="25%" paginated backend-pagination pagination-position="both" :td-attrs="$utils.tdID"
        @page-change="onPageChange">
        <div>
          <a :href="`/lists/${props.row.id}`" @click.prevent="showEditForm(props.row)">
            {{ props.row.name }}
          </a>
          <b-taglist class="team-badges">
            <b-tag v-for="t in teamTagsOf(props.row)" :key="t" type="is-info is-light" class="team-badge" data-cy="team-badge">
              <b-icon icon="account-multiple" size="is-small" /> {{ teamName(t) }}
            </b-tag>
          </b-taglist>
          <b-taglist>
            <b-tag class="is-small" v-for="t in plainTagsOf(props.row)" :key="t">
              {{ t }}
            </b-tag>
          </b-taglist>
          <b-tag v-if="drawOf(props.row)" type="is-info is-light" class="drawn-from" data-cy="drawn-from"
            :title="drawTitle(drawOf(props.row))">
            {{ drawBadge(drawOf(props.row)) }}
          </b-tag>
        </div>
      </b-table-column>

      <b-table-column v-slot="props" field="type" :label="$t('globals.fields.type')" header-class="cy-type" sortable
        width="15%">
        <div class="tags">
          <b-tag v-if="props.row.axmFilter" type="is-info is-light" data-cy="type-dynamic">{{ $t('lists.dynamic.dynamic') }}</b-tag>
          <b-tag v-else :class="props.row.type" :data-cy="`type-${props.row.type}`">
            {{ $t(`lists.types.${props.row.type}`) }}
          </b-tag>
          {{ ' ' }}

          <b-tag v-if="!props.row.axmFilter" :class="props.row.optin" :data-cy="`optin-${props.row.optin}`">
            <b-icon :icon="props.row.optin === 'double' ? 'account-check-outline' : 'account-off-outline'"
              size="is-small" />
            {{ ' ' }}
            {{ $t(`lists.optins.${props.row.optin}`) }}
          </b-tag>{{ ' ' }}

          <a v-if="!props.row.axmFilter && props.row.optin === 'double'" class="is-size-7 send-optin" href="#"
            @click="$utils.confirm(null, () => createOptinCampaign(props.row))" data-cy="btn-send-optin-campaign">
            <b-tooltip :label="$t('lists.sendOptinCampaign')" type="is-dark">
              <b-icon icon="rocket-launch-outline" size="is-small" />
              {{ $t('lists.sendOptinCampaign') }}
            </b-tooltip>
          </a>
        </div>
      </b-table-column>

      <b-table-column v-slot="props" field="subscriber_count" :label="$t('globals.terms.subscribers')"
        header-class="cy-subscribers" numeric sortable centered>
        <template v-if="$can('subscribers:get_all', 'subscribers:get')">
          <router-link :to="`/subscribers/lists/${props.row.id}`">
            {{ $utils.formatNumber(props.row.subscriberCount) }}
            <span class="is-size-7 view">{{ $t('globals.buttons.view') }}</span>
          </router-link>
        </template>
        <template v-else>
          {{ $utils.formatNumber(props.row.subscriberCount) }}
        </template>
      </b-table-column>

      <b-table-column v-slot="props" field="subscriber_counts" header-class="cy-subscribers" width="10%">
        <div class="fields stats">
          <p v-for="(count, status) in filterStatuses(props.row)" :key="status">
            <label for="#">{{ $tc(`subscribers.status.${status}`, count) }}</label>
            <router-link :to="`/subscribers/lists/${props.row.id}?subscription_status=${status}`" :class="status">
              {{ $utils.formatNumber(count) }}
            </router-link>
          </p>
        </div>
      </b-table-column>

      <b-table-column v-slot="props" field="created_at" :label="$t('globals.fields.createdAt')"
        header-class="cy-created_at" sortable>
        {{ $utils.niceDate(props.row.createdAt) }}
      </b-table-column>
      <b-table-column v-slot="props" field="updated_at" :label="$t('globals.fields.updatedAt')"
        header-class="cy-updated_at" sortable>
        {{ $utils.niceDate(props.row.updatedAt) }}
      </b-table-column>

      <b-table-column v-slot="props" cell-class="actions" align="right">
        <div>
          <router-link v-if="$can('campaigns:manage')" :to="`/campaigns/new?list_id=${props.row.id}`"
            data-cy="btn-campaign">
            <b-tooltip :label="$t('lists.sendCampaign')" type="is-dark">
              <b-icon icon="rocket-launch-outline" size="is-small" />
            </b-tooltip>
          </router-link>

          <a v-if="canDraw(props.row)" href="#" @click.prevent="showDrawForm(props.row)" data-cy="btn-draw"
            :aria-label="$t('lists.draw.action')">
            <b-tooltip :label="$t('lists.draw.action')" type="is-dark">
              <b-icon icon="account-search-outline" size="is-small" />
            </b-tooltip>
          </a>

          <a v-if="$can('lists:manage') || $canList(props.row.id, 'list:manage')" href="#"
            @click.prevent="showEditForm(props.row)" data-cy="btn-edit" :aria-label="$t('globals.buttons.edit')">
            <b-tooltip :label="$t('globals.buttons.edit')" type="is-dark">
              <b-icon icon="pencil-outline" size="is-small" />
            </b-tooltip>
          </a>

          <router-link v-if="$can('subscribers:import')" :to="{ name: 'import', query: { list_id: props.row.id } }"
            data-cy="btn-import">
            <b-tooltip :label="$t('import.title')" type="is-dark">
              <b-icon icon="file-upload-outline" size="is-small" />
            </b-tooltip>
          </router-link>

          <a v-if="$can('lists:manage') || $canList(props.row.id, 'list:manage')" href="#"
            @click.prevent="deleteList(props.row)" data-cy="btn-delete" :aria-label="$t('globals.buttons.delete')">
            <b-tooltip :label="$t('globals.buttons.delete')" type="is-dark">
              <b-icon icon="trash-can-outline" size="is-small" />
            </b-tooltip>
          </a>
        </div>
      </b-table-column>

      <template #empty v-if="!loading.listsFull">
        <empty-placeholder />
      </template>
    </b-table>

    <!-- Add / edit form modal -->
    <b-modal custom-class="axm-list-modal" has-modal-card scroll="keep" :aria-modal="true" :active.sync="isFormVisible" :width="860" @close="onFormClose">
      <list-form :key="curItem ? curItem.id : 0" :data="curItem" :is-editing="isEditing" :teams="teams" @finished="formFinished" @draw="showDrawForm"
        @open-list="openList" />
    </b-modal>

    <!-- Draw N people into a new list -->
    <b-modal has-modal-card scroll="keep" :aria-modal="true" :active.sync="isDeleteVisible" :width="560"
      @close="deleteAsk = { lists: [], total: 0, fn: null }">
      <list-delete-confirm :lists="deleteAsk.lists" :total="deleteAsk.total" :trash-days="trashDays"
        @confirm="onDeleteConfirmed" />
    </b-modal>

    <b-modal custom-class="axm-list-modal" has-modal-card scroll="keep" :aria-modal="true" :active.sync="isDrawVisible"
      :width="680" @close="onDrawClose">
      <list-draw v-if="drawSource" :source="drawSource" :teams="teams" @finished="formFinished" @open-list="openList" />
    </b-modal>

    <p v-if="settings['app.cache_slow_queries']" class="has-text-grey">
      *{{ $t('globals.messages.slowQueriesCached') }}
      <a href="https://listmonk.app/docs/maintenance/performance/" target="_blank" rel="noopener noreferer"
        class="has-text-grey">
        <b-icon icon="link-variant" /> {{ $t('globals.buttons.learnMore') }}
      </a>
    </p>
  </section>
</template>

<script>
import Vue from 'vue';
import { mapState } from 'vuex';
import EmptyPlaceholder from '../components/EmptyPlaceholder.vue';
import ListDraw from '../components/ListDraw.vue';
import ListDeleteConfirm from '../components/ListDeleteConfirm.vue';
import ListForm from './ListForm.vue';
import { DRAW_TAG } from '../list-draw';
import {
  NO_TEAM, defaultTeamTag, plainTags, teamName, teamTags,
} from '../teams';

export default Vue.extend({
  components: {
    ListForm,
    ListDraw,
    ListDeleteConfirm,
    EmptyPlaceholder,
  },

  data() {
    return {
      // Current list item being edited.
      curItem: null,

      // The delete confirmation (LST-27) and the days a deleted list stays in the trash.
      isDeleteVisible: false,
      deleteAsk: { lists: [], total: 0, fn: null },
      trashDays: 14,
      isEditing: false,
      isFormVisible: false,
      lists: [],
      queryParams: {
        page: 1,
        query: '',
        orderBy: 'id',
        order: 'asc',
        status: this.$route.query.status || 'active',
        // Only the lists made by a draw (they carry the draw tag).
        drawn: this.$route.query.drawn === '1',
        // A team tag, NO_TEAM (lists without a team) or '' (all).
        team: this.$route.query.team || '',
      },

      // The teams on the lists the user may read, and (admins) how many lists have no team.
      teams: [],
      teamless: 0,
      NO_TEAM,

      // Draw records of the lists on this page, by drawn list ID.
      draws: {},
      isDrawVisible: false,
      drawSource: null,

      // Table bulk row selection states.
      bulk: {
        checked: [],
        all: false,
      },
    };
  },

  methods: {
    onPageChange(p) {
      this.queryParams.page = p;
      this.getLists();
    },

    onSort(field, direction) {
      this.queryParams.orderBy = field;
      this.queryParams.order = direction;
      this.getLists();
    },

    // Show the edit list form.
    showEditForm(list) {
      // GET /api/lists/:id adds the status counts to the total again for an ordinary list;
      // the form (and the draw dialog opened from it) gets the real count whatever the path.
      const item = { ...list };
      if (!item.axmFilter && item.subscriberStatuses) {
        item.subscriberCount = Object.values(item.subscriberStatuses).reduce((a, b) => a + b, 0);
      }
      this.curItem = item;
      this.isFormVisible = true;
      this.isEditing = true;
    },

    // Show the new list form.
    showNewForm() {
      // A team filter in use is the team a new list is most likely for.
      const asked = this.queryParams.team && this.queryParams.team !== NO_TEAM ? [this.queryParams.team] : [];
      this.curItem = { defaultTeam: defaultTeamTag(asked, this.teams) };
      this.isFormVisible = true;
      this.isEditing = false;
    },

    formFinished() {
      this.getLists();
      this.loadTeams();
      // A team member's lists changed: the permission checks need the new ones.
      this.$refreshProfile();
    },

    // Drawing needs the permission to make lists (or being on a team), read access to the
    // list, and a list that is not archived or itself drawn.
    canDraw(list) {
      return this.$canCreateList() && (this.$can('lists:manage_all') || this.$canList(list.id, 'list:get'))
        && list.status !== 'archived' && !this.drawOf(list);
    },

    teamName,
    teamTagsOf(list) { return teamTags(list.tags); },
    plainTagsOf(list) { return plainTags(list.tags); },

    loadTeams() {
      this.$api.getListTeams().then((out) => {
        this.teams = out.teams || [];
        this.teamless = out.teamless || 0;
      }).catch(() => { this.teams = []; this.teamless = 0; });
    },

    onTeamFilter() { this.setTeam(this.queryParams.team); },

    setTeam(team) {
      this.queryParams.team = team;
      this.queryParams.page = 1;
      this.syncRoute();
      this.getLists();
    },

    // The filters stay in the address so a link (or a reload) keeps them.
    syncRoute() {
      const query = {};
      if (this.queryParams.drawn) query.drawn = '1';
      if (this.queryParams.team) query.team = this.queryParams.team;
      if (this.queryParams.status === 'archived') query.status = 'archived';
      this.$router.replace({ name: 'lists', query }).catch(() => {});
    },

    drawOf(list) { return this.draws[list.id] || null; },

    drawBadge(d) {
      return this.$t('lists.draw.drawnBadge', {
        name: d.sourceName, time: this.$utils.niceDate(d.drawnAt, true), num: this.$utils.formatNumber(d.result.drawn || 0),
      });
    },

    drawTitle(d) {
      return this.$t('lists.draw.drawnTitle', {
        name: d.sourceName, time: this.$utils.niceDate(d.drawnAt, true), num: this.$utils.formatNumber(d.result.drawn || 0),
      });
    },

    showDrawForm(list) {
      // Asked from inside the edit form: close that one first.
      this.isFormVisible = false;
      this.drawSource = list;
      this.isDrawVisible = true;
    },

    onDrawClose() {
      this.drawSource = null;
    },

    // Opens the edit form of a list by ID (a drawn list or a source, from the draw screens).
    openList(id) {
      this.isDrawVisible = false;
      this.$api.getList(id).then((data) => this.showEditForm(data));
    },

    setDrawn(on) {
      this.queryParams.drawn = on;
      this.queryParams.page = 1;
      this.syncRoute();
      this.getLists();
    },

    // The draw records of the lists on the page, to mark the drawn ones.
    loadDraws(results) {
      const ids = (results || []).map((l) => l.id);
      if (!ids.length) { this.draws = {}; return; }
      this.$api.getListDraws({ list_id: ids }).then((rows) => {
        const out = {};
        (rows || []).forEach((r) => { if (r.listId) out[r.listId] = r; });
        this.draws = out;
      }).catch(() => { this.draws = {}; });
    },

    onFormClose() {
      if (this.$route.params.id) {
        this.$router.push({ name: 'lists' });
      }
    },

    filterStatuses(list) {
      const out = { ...list.subscriberStatuses };
      if (list.optin === 'single') {
        delete out.unconfirmed;
        delete out.confirmed;
      }
      return out;
    },

    getLists() {
      this.loadTeams();
      this.$api.queryLists({
        page: this.queryParams.page,
        query: this.queryParams.query.replace(/[^\p{L}\p{N}\s]/gu, ' '),
        order_by: this.queryParams.orderBy,
        order: this.queryParams.order,
        status: this.queryParams.status,
        ...(this.teamParams()),
      }).then((resp) => {
        this.lists = resp;
        this.loadDraws(resp.results);
      });

      // Also fetch the minimal lists for the global store that appears
      // in dropdown menus on other pages like import and campaigns.
      this.$api.getLists({ minimal: true, per_page: 'all', status: 'active' });
    },

    // The team filter and the draw tag are tag conditions; "no team" is its own parameter.
    teamParams() {
      const tags = [];
      if (this.queryParams.drawn) tags.push(DRAW_TAG);
      const out = {};
      if (this.queryParams.team === NO_TEAM) out.team = 'none';
      else if (this.queryParams.team) tags.push(this.queryParams.team);
      if (tags.length) out.tag = tags;
      return out;
    },

    deleteList(list) {
      this.askDelete([list], 1, () => {
        this.$api.deleteList(list.id).then(() => {
          this.getLists();

          this.$utils.toast(this.$t('lists.delete.done', { name: list.name, days: this.trashDays }));
        });
      });
    },

    // Opens the confirmation that asks for the name (one list) or the number of lists, and
    // runs fn when it is given.
    askDelete(lists, total, fn) {
      this.deleteAsk = { lists, total, fn };
      this.isDeleteVisible = true;
    },

    onDeleteConfirmed() {
      const { fn } = this.deleteAsk;
      this.deleteAsk = { lists: [], total: 0, fn: null };
      if (fn) fn();
    },

    // Mark all lists in the query as selected.
    onSelectAll() {
      this.bulk.all = true;
    },

    onTableCheck() {
      // Disable bulk.all selection if there are no rows checked in the table.
      if (this.bulk.checked.length !== this.lists.total) {
        this.bulk.all = false;
      }
    },

    deleteLists() {
      const fn = () => {
        const params = {};
        if (!this.bulk.all && this.bulk.checked.length > 0) {
          // If 'all' is not selected, delete lists by IDs.
          params.id = this.bulk.checked.map((l) => l.id);
        } else {
          // 'All' is selected, delete by query.
          params.query = this.queryParams.query.replace(/[^\p{L}\p{N}\s]/gu, ' ');
          params.all = this.bulk.all;
        }

        this.$api.deleteLists(params)
          .then(() => {
            this.getLists();
            this.$utils.toast(this.$t('lists.delete.doneMany', { num: this.numSelectedLists, days: this.trashDays }));
          });
      };

      const byIDs = !this.bulk.all && this.bulk.checked.length > 0;
      if (byIDs) {
        this.askDelete(this.bulk.checked, this.numSelectedLists, fn);
        return;
      }

      // Every list a search finds: read them, so the confirmation can name them.
      const query = this.queryParams.query.replace(/[^\p{L}\p{N}\s]/gu, ' ').trim();
      this.$api.getListsForDelete({ query }).then((resp) => {
        this.askDelete(resp.results, resp.results.length, fn);
      }).catch(() => {
        this.askDelete([], this.numSelectedLists, fn);
      });
    },

    createOptinCampaign(list) {
      const data = {
        name: this.$t('lists.optinTo', { name: list.name }),
        subject: this.$t('lists.confirmSub', { name: list.name }),
        lists: [list.id],
        from_email: this.settings['app.from_email'],
        content_type: 'richtext',
        messenger: 'email',
        type: 'optin',
      };

      this.$api.createCampaign(data).then((d) => {
        this.$router.push({ name: 'campaign', hash: '#content', params: { id: d.id } });
      });
      return false;
    },
  },

  computed: {
    ...mapState(['loading', 'settings']),

    myTeam() { return this.$teamTag(); },
    myTeamName() { return teamName(this.myTeam); },

    // Only people who read lists of several teams (admins) have a team to choose.
    showTeamFilter() { return !this.myTeam && this.teamOptions.length > 0; },
    teamOptions() { return this.teams; },

    numSelectedLists() {
      return this.bulk.all ? this.lists.total : this.bulk.checked.length;
    },
  },

  created() {
    this.$root.$on('page.refresh', this.getLists);
  },

  destroyed() {
    this.$root.$off('page.refresh', this.getLists);
  },

  mounted() {
    if (this.$route.params.id) {
      this.$api.getList(parseInt(this.$route.params.id, 10)).then((data) => {
        this.showEditForm(data);
      });
    } else {
      this.getLists();
    }
  },
});
</script>

<style>
.axm-list-modal .animation-content { width: calc(100vw - 20px); }
/* The draw badge is long: let it wrap inside its cell instead of widening the page. */
.team-badges { margin: 4px 0 0; }
.team-badge { margin-bottom: 2px; }
.my-team { vertical-align: middle; margin-left: 8px; }
.tag.drawn-from { height: auto; max-width: 100%; white-space: normal; padding: 2px 8px; line-height: 1.4; margin-top: 4px; }
</style>
