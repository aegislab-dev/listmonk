<template>
  <section class="subscribers">
    <header class="columns page-header">
      <div class="column is-10">
        <h1 class="title is-4">
          {{ $t('globals.terms.subscribers') }}
          <span v-if="!isNaN(subscribers.total)">
            (<span data-cy="count">{{ subscribers.total }}</span>)
          </span>
          <span v-if="currentList">
            &raquo; {{ currentList.name }}
            <span v-if="queryParams.subStatus" class="has-text-grey has-text-weight-normal is-capitalized">({{
              queryParams.subStatus }})</span>
          </span>
        </h1>
      </div>
      <div class="column has-text-right">
        <b-field v-if="$can('subscribers:manage')" expanded>
          <b-button expanded type="is-primary" icon-left="plus" @click="showNewForm" data-cy="btn-new" class="btn-new">
            {{ $t('globals.buttons.new') }}
          </b-button>
        </b-field>
      </div>
    </header>

    <p v-if="currentList && currentList.axmFilter" class="notification is-info is-light" data-cy="dynamic-members-help">
      {{ $t('lists.dynamic.membersHelp') }}
    </p>
    <section class="subscribers-controls">
      <div class="columns">
        <div class="column is-8">
          <form @submit.prevent="onSubmit">
            <div>
              <b-field addons>
                <b-input @input="onSimpleQueryInput" v-model="queryInput" expanded
                  :placeholder="$t('subscribers.queryPlaceholder')" icon="magnify" ref="query"
                  :disabled="isSearchAdvanced" data-cy="search" />
                <p class="controls">
                  <b-button native-type="submit" type="is-primary" icon-left="magnify" :disabled="isSearchAdvanced"
                    data-cy="btn-search" />
                </p>
              </b-field>

              <div v-if="isSearchAdvanced">
                <b-input v-model="queryParams.queryExp" @keydown.native.enter="onAdvancedQueryEnter" type="textarea"
                  ref="queryExp" placeholder="subscribers.name LIKE '%user%' or subscribers.status='blocklisted'"
                  data-cy="query" />
                <span class="is-size-6 has-text-grey">
                  {{ $t('subscribers.advancedQueryHelp') }}.{{ ' ' }}
                  <a href="https://listmonk.app/docs/querying-and-segmentation" target="_blank"
                    rel="noopener noreferrer">
                    {{ $t('globals.buttons.learnMore') }}.
                  </a>
                </span>
                <div class="buttons">
                  <b-button native-type="submit" type="is-primary" icon-left="magnify" data-cy="btn-query">
                    {{
                      $t('subscribers.query') }}
                  </b-button>
                  <b-button @click.prevent="toggleAdvancedSearch" icon-left="cancel" data-cy="btn-query-reset">
                    {{ $t('subscribers.reset') }}
                  </b-button>
                </div>
              </div><!-- advanced query -->
            </div>
          </form>
          <div v-if="!isSearchAdvanced" class="toggle-advanced">
            <a href="#" @click.prevent="toggleAdvancedSearch" data-cy="btn-advanced-search">
              <b-icon icon="cog-outline" size="is-small" />
              {{ $t('subscribers.advancedQuery') }}
            </a>
          </div>
        </div><!-- search -->
      </div>
    </section><!-- control -->

    <section v-if="attribFilters.length || filterForm" class="attrib-filters" data-cy="attrib-filters">
      <div v-if="attribFilters.length" class="attrib-chips">
        <span class="attrib-chips-label">{{ $t('subscribers.columns.filterTitle') }}</span>
        <span v-for="(f, i) in attribFilters" :key="`${f.key}-${f.op}-${i}`" class="tag is-medium attrib-chip"
          :data-cy="`attrib-chip-${i}`">
          {{ chipText(f) }}
          <button type="button" class="delete is-small" :aria-label="$t('subscribers.columns.filterRemove', { label: chipText(f) })"
            @click="removeFilter(i)" />
        </span>
        <a v-if="attribFilters.length > 1" href="#" class="attrib-chips-clear" @click.prevent="clearFilters"
          data-cy="attrib-filter-clear">
          {{ $t('globals.buttons.clearAll') }}
        </a>
      </div>
      <form v-if="filterForm" class="attrib-filter-form" @submit.prevent="applyFilter" data-cy="attrib-filter-form">
        <b-field :label="$t('lists.dynamic.key')" label-for="attrib-filter-key">
          <b-autocomplete v-model="filterForm.key" :data="filterKeyOptions" maxlength="64" :open-on-focus="true"
            :keep-first="false" id="attrib-filter-key" ref="filterKey" :placeholder="$t('lists.dynamic.keyPlaceholder')"
            data-cy="attrib-filter-key" />
        </b-field>
        <b-field :label="$t('lists.dynamic.operator')" label-for="attrib-filter-op">
          <b-select v-model="filterForm.op" expanded id="attrib-filter-op" data-cy="attrib-filter-op"
            @input="filterError = ''">
            <optgroup :label="$t('lists.dynamic.opsText')">
              <option v-for="op in filterOps.text" :key="op" :value="op">{{ $t(`lists.dynamic.ops.${op}`) }}</option>
            </optgroup>
            <optgroup :label="$t('lists.dynamic.opsNumber')">
              <option v-for="op in filterOps.number" :key="op" :value="op">{{ $t(`lists.dynamic.ops.${op}`) }}</option>
            </optgroup>
          </b-select>
        </b-field>
        <b-field :label="$t('lists.dynamic.value')" label-for="attrib-filter-value"
          :type="filterError ? 'is-danger' : ''" :message="filterError">
          <b-input v-if="filterForm.op !== 'empty'" v-model="filterForm.value" id="attrib-filter-value" ref="filterValue"
            :maxlength="isNumberOp(filterForm.op) ? 64 : 200" :has-counter="false"
            :inputmode="isNumberOp(filterForm.op) ? 'decimal' : 'text'" autocomplete="off"
            :placeholder="$t(isNumberOp(filterForm.op) ? 'lists.dynamic.numberPlaceholder' : 'lists.dynamic.valuePlaceholder')"
            @input="filterError = ''" data-cy="attrib-filter-value" />
          <span v-else class="attrib-filter-novalue">{{ $t('lists.dynamic.noValue') }}</span>
        </b-field>
        <div class="attrib-filter-buttons">
          <b-button native-type="submit" type="is-primary" data-cy="attrib-filter-apply">
            {{ $t('subscribers.columns.filterApply') }}
          </b-button>
          <b-button @click="closeFilter" data-cy="attrib-filter-cancel">{{ $t('globals.buttons.cancel') }}</b-button>
        </div>
        <p class="help attrib-filter-help">{{ $t('subscribers.columns.filterHelp') }}</p>
      </form>
    </section>

    <br v-else />
    <p v-if="isTableWide" class="help table-wide-hint" data-cy="table-wide-hint">
      {{ $t('subscribers.columns.wideHint') }}
    </p>
    <b-table ref="table" :data="subscribers.results ?? []" :loading="loading.subscribers" @check-all="onTableCheck"
      @check="onTableCheck" :checked-rows.sync="bulk.checked" paginated backend-pagination pagination-position="both"
      @page-change="onPageChange" :current-page="queryParams.page" :per-page="subscribers.perPage"
      :total="subscribers.total" hoverable checkable backend-sorting @sort="onSort" class="subscribers-table"
      :mobile-sort-placeholder="$t('subscribers.columns.sortBy')">
      <template #top-left>
        <div class="actions">
          <a class="a" href="#" @click.prevent="exportSubscribers" data-cy="btn-export-subscribers">
            <b-icon icon="cloud-download-outline" size="is-small" />
            {{ $t('subscribers.export') }}
          </a>
          <b-dropdown ref="columnPicker" class="a column-picker" v-bind="{ 'aria-role': 'dialog' }" :close-on-click="false"
            :mobile-modal="true" trap-focus @active-change="onPickerToggle">
            <template #trigger>
              <a href="#" @click.prevent role="button" data-cy="btn-columns">
                <svg class="axm-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
                  <path d="M4 5h16v14H4zM9.5 5v14M14.5 5v14" />
                </svg>
                {{ $t('subscribers.columns.pick') }}
                <span v-if="columns.attribs.length" class="column-count">{{ columns.attribs.length }}</span>
              </a>
            </template>
            <b-dropdown-item custom :focusable="false" class="column-picker-body">
              <div data-cy="column-picker">
                <p class="column-picker-title">{{ $t('subscribers.columns.title') }}</p>
                <p class="column-picker-group">{{ $t('subscribers.columns.base') }}</p>
                <div class="column-picker-base">
                  <b-checkbox :value="true" disabled>{{ $t('subscribers.email') }}</b-checkbox>
                  <b-checkbox :value="true" disabled>{{ $t('globals.fields.name') }}</b-checkbox>
                  <b-checkbox v-for="c in baseColumns" :key="c.id" :value="!columns.hidden.includes(c.id)"
                    @input="toggleBaseColumn(c.id)" :data-cy="`column-base-${c.id}`">
                    {{ c.label }}
                  </b-checkbox>
                </div>
                <p class="column-picker-group">
                  {{ $t('subscribers.columns.attribs') }}
                  <span class="column-picker-limit">{{ columns.attribs.length }} / {{ maxAttribColumns }}</span>
                </p>
                <p v-if="columns.auto && columns.attribs.length" class="help column-picker-auto" data-cy="column-auto">
                  {{ $t('subscribers.columns.autoNote') }}
                </p>
                <ul v-if="pickerKeys.length" class="column-picker-keys">
                  <li v-for="k in pickerKeys" :key="k.key">
                    <b-checkbox :value="columns.attribs.includes(k.key)" @input="toggleAttribColumn(k.key)"
                      :disabled="!columns.attribs.includes(k.key) && columns.attribs.length >= maxAttribColumns"
                      :data-cy="`column-attrib-${k.key}`">
                      <span class="column-key">{{ k.key }}</span>
                    </b-checkbox>
                    <span class="column-filled">{{ $t('subscribers.columns.filled', { num: k.filled }) }}</span>
                  </li>
                </ul>
                <p v-else class="help">{{ $t('subscribers.columns.none') }}</p>
                <p class="help">{{ $t('subscribers.columns.found', { num: pageSize }) }}</p>
                <form class="column-picker-custom" @submit.prevent="addCustomColumn">
                  <b-field :label="$t('subscribers.columns.custom')" label-for="column-custom-key">
                    <b-input v-model="customKey" maxlength="64" :has-counter="false" expanded id="column-custom-key"
                      :placeholder="$t('subscribers.columns.customPlaceholder')" data-cy="column-custom-key" />
                    <p class="control">
                      <b-button native-type="submit" data-cy="column-custom-add"
                        :disabled="!customKeyClean || columns.attribs.length >= maxAttribColumns">
                        {{ $t('globals.buttons.add') }}
                      </b-button>
                    </p>
                  </b-field>
                </form>
                <p class="column-picker-foot">
                  <a href="#" @click.prevent="resetColumns" data-cy="column-reset">{{ $t('subscribers.columns.reset') }}</a>
                  <a href="#" class="column-picker-close" @click.prevent="$refs.columnPicker.toggle()">
                    {{ $t('globals.buttons.close') }}
                  </a>
                </p>
                <p class="help">{{ $t('subscribers.columns.remembered') }}</p>
              </div>
            </b-dropdown-item>
          </b-dropdown>
          <a class="a" href="#" @click.prevent="openFilter('')" data-cy="btn-attrib-filter">
            <svg class="axm-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
              <path d="M4 5h16l-6.2 7.6V19l-3.6-2v-4.4z" />
            </svg>
            {{ $t('subscribers.columns.filterAdd') }}
          </a>
          <template v-if="bulk.checked.length > 0">
            <a v-if="!currentList || !currentList.axmFilter" class="a" href="#" @click.prevent="showBulkListForm" data-cy="btn-manage-lists">
              <b-icon icon="format-list-bulleted-square" size="is-small" /> Manage lists
            </a>
            <a class="a" href="#" @click.prevent="deleteSubscribers" data-cy="btn-delete-subscribers">
              <b-icon icon="trash-can-outline" size="is-small" /> Delete
            </a>
            <a class="a" href="#" @click.prevent="blocklistSubscribers" data-cy="btn-manage-blocklist">
              <b-icon icon="account-off-outline" size="is-small" /> Blocklist
            </a>
            <span class="a">
              {{ $t('globals.messages.numSelected', { num: numSelectedSubscribers }) }}
              <span v-if="!bulk.all && subscribers.total > subscribers.perPage">
                &mdash;
                <a href="#" @click.prevent="selectAllSubscribers">
                  {{ $t('globals.messages.selectAll', { num: subscribers.total }) }}
                </a>
              </span>
            </span>
          </template>
        </div>
      </template>

      <b-table-column v-slot="props" field="email" :label="$t('subscribers.email')" header-class="cy-email" sortable
        cell-class="sub-email-cell" :td-attrs="$utils.tdID">
        <a :href="`/subscribers/${props.row.id}`" @click.prevent="showEditForm(props.row)"
          :class="{ 'blocklisted': props.row.status === 'blocklisted' }">
          {{ props.row.email }}
          <copy-text :text="`${props.row.email}`" hide-text />
        </a>
        <b-tag v-if="props.row.status !== 'enabled'" :class="props.row.status" data-cy="blocklisted">
          {{ $t(`subscribers.status.${props.row.status}`) }}
        </b-tag>
        <b-taglist>
          <template v-for="l in props.row.lists">
            <router-link :to="`/subscribers/lists/${l.id}`" :key="l.id" style="padding-right:0.5em;">
              <b-tag :class="l.subscriptionStatus" size="is-small" :key="l.id">
                {{ l.name }}
                <sup v-if="l.optin === 'double' || l.subscriptionStatus == 'unsubscribed'">
                  {{ $t(`subscribers.status.${l.subscriptionStatus}`) }}
                </sup>
              </b-tag>
            </router-link>
          </template>
        </b-taglist>
      </b-table-column>

      <b-table-column v-slot="props" field="name" :label="$t('globals.fields.name')" header-class="cy-name" sortable>
        <a :href="`/subscribers/${props.row.id}`" @click.prevent="showEditForm(props.row)"
          :class="{ 'blocklisted': props.row.status === 'blocklisted' }">
          {{ props.row.name }}
          <copy-text :text="`${props.row.name}`" hide-text />
        </a>
      </b-table-column>

      <b-table-column v-slot="props" field="lists" :label="$t('globals.terms.lists')" header-class="cy-lists" centered
        :visible="!columns.hidden.includes('lists')">
        {{ listCount(props.row.lists) }}
      </b-table-column>

      <b-table-column v-for="key in columns.attribs" :key="`attrib-${key}`" :field="`attribs.${key}`" :label="key"
        header-class="attrib-head" cell-class="attrib-cell" sortable>
        <template #header="{ column }">
          <span class="attrib-head-label" :title="$t('subscribers.columns.headTitle', { key })">{{ column.label }}</span>
          <button type="button" class="attrib-head-filter" :class="{ 'is-active': hasFilter(key) }"
            :aria-label="$t('subscribers.columns.filterBy', { key })" :title="$t('subscribers.columns.filterBy', { key })"
            @click.stop="openFilter(key)" :data-cy="`attrib-head-filter-${key}`">
            <svg class="axm-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
              <path d="M4 5h16l-6.2 7.6V19l-3.6-2v-4.4z" />
            </svg>
          </button>
        </template>
        <template #default="props">
          <span v-if="!attribCell(props.row, key).text" class="attrib-empty" :aria-label="$t('lists.dynamic.sampleNoValue')">&mdash;</span>
          <span v-else class="attrib-wrap">
            <span class="attrib-value" :class="{ 'is-open': isCellOpen(props.row, key) }"
              :title="attribCell(props.row, key).text">{{ attribCell(props.row, key).text }}</span>
            <a v-if="attribCell(props.row, key).long" href="#" class="attrib-more"
              @click.prevent="toggleCell(props.row, key)" :aria-expanded="isCellOpen(props.row, key) ? 'true' : 'false'">
              {{ $t(isCellOpen(props.row, key) ? 'subscribers.columns.less' : 'subscribers.columns.more') }}
            </a>
          </span>
        </template>
      </b-table-column>

      <b-table-column v-slot="props" field="created_at" :label="$t('globals.fields.createdAt')"
        header-class="cy-created_at" sortable cell-class="sub-date-cell"
        :visible="!columns.hidden.includes('created_at')">
        {{ $utils.niceDate(props.row.createdAt) }}
      </b-table-column>

      <b-table-column v-slot="props" field="updated_at" :label="$t('globals.fields.updatedAt')"
        header-class="cy-updated_at" sortable cell-class="sub-date-cell"
        :visible="!columns.hidden.includes('updated_at')">
        {{ $utils.niceDate(props.row.updatedAt) }}
      </b-table-column>

      <b-table-column v-slot="props" cell-class="actions" align="right">
        <div>
          <a :href="`/api/subscribers/${props.row.id}/export`" data-cy="btn-download"
            :aria-label="$t('subscribers.downloadData')">
            <b-tooltip :label="$t('subscribers.downloadData')" type="is-dark">
              <b-icon icon="cloud-download-outline" size="is-small" />
            </b-tooltip>
          </a>
          <a v-if="$can('subscribers:manage')" :href="`/subscribers/${props.row.id}`"
            @click.prevent="showEditForm(props.row)" data-cy="btn-edit" :aria-label="$t('globals.buttons.edit')">
            <b-tooltip :label="$t('globals.buttons.edit')" type="is-dark">
              <b-icon icon="pencil-outline" size="is-small" />
            </b-tooltip>
          </a>
          <a v-if="$can('subscribers:manage')" href="#" @click.prevent="deleteSubscriber(props.row)"
            data-cy="btn-delete" :aria-label="$t('globals.buttons.delete')">
            <b-tooltip :label="$t('globals.buttons.delete')" type="is-dark">
              <b-icon icon="trash-can-outline" size="is-small" />
            </b-tooltip>
          </a>
        </div>
      </b-table-column>

      <template #empty v-if="!loading.subscribers">
        <empty-placeholder />
      </template>
    </b-table>

    <!-- Manage list modal -->
    <b-modal scroll="keep" :aria-modal="true" :active.sync="isBulkListFormVisible" :width="500" class="has-overflow">
      <subscriber-bulk-list :num-subscribers="this.numSelectedSubscribers" @finished="bulkChangeLists" />
    </b-modal>

    <!-- Add / edit form modal -->
    <b-modal scroll="keep" :aria-modal="true" :active.sync="isFormVisible" :width="850" @close="onFormClose">
      <subscriber-form :data="curItem" :is-editing="isEditing" @finished="querySubscribers" />
    </b-modal>
  </section>
</template>

<script>
import Vue from 'vue';
import { mapState } from 'vuex';
import EmptyPlaceholder from '../components/EmptyPlaceholder.vue';
import { uris } from '../constants';
import SubscriberBulkList from './SubscriberBulkList.vue';
import SubscriberForm from './SubscriberForm.vue';
import CopyText from '../components/CopyText.vue';
import { isNumberOp } from '../dynamic-list';
import {
  BASE_COLUMNS, FILTER_OPS, LONG_VALUE, MAX_ATTRIB_COLUMNS, MAX_CONDITIONS, attribStats, attribText, cleanCondition,
  cleanKey, conditionError, conditionValueText, loadColumns, newCondition, saveColumns, suggestColumns,
} from '../subscriber-columns';

export default Vue.extend({
  components: {
    SubscriberForm,
    SubscriberBulkList,
    CopyText,
    EmptyPlaceholder,
  },

  data() {
    return {
      // Current subscriber item being edited.
      curItem: null,
      isSearchAdvanced: false,
      isEditing: false,
      isFormVisible: false,
      isBulkListFormVisible: false,

      // Table bulk row selection states.
      bulk: {
        checked: [],
        all: false,
      },

      queryInput: '',

      // Columns of the table. attribs are attribute keys shown as columns, hidden are base
      // column IDs turned off. auto means the user has not picked columns for this list yet.
      columns: {
        attribs: [], hidden: [], auto: true, suggested: false,
      },
      // Attribute keys found in the subscribers of the current page, with how many have a value.
      columnStats: [],
      customKey: '',
      // Long values opened in place, keyed by subscriber ID and attribute key.
      openCells: {},
      maxAttribColumns: MAX_ATTRIB_COLUMNS,
      filterOps: FILTER_OPS,

      // Attribute conditions applied to the table, and the condition being typed (or null).
      attribFilters: [],
      filterForm: null,
      filterError: '',

      // True while the table is wider than its box and scrolls sideways.
      isTableWide: false,

      // Query params to filter the getSubscribers() API call.
      queryParams: {
        // Search query expression.
        queryExp: '',
        search: '',

        // ID of the list the current subscriber view is filtered by.
        listID: null,
        page: 1,
        orderBy: 'id',
        order: 'desc',
        subStatus: null,
      },
    };
  },

  methods: {
    isNumberOp,

    // The saved columns belong to one list; the page without a list has its own.
    columnsKey() {
      return this.queryParams.listID || 'all';
    },

    saveColumnChoice() {
      this.columns.auto = false;
      saveColumns(window.localStorage, this.columnsKey(), this.columns);
    },

    toggleAttribColumn(key) {
      const on = this.columns.attribs.includes(key);
      if (!on && this.columns.attribs.length >= MAX_ATTRIB_COLUMNS) return;
      this.columns.attribs = on ? this.columns.attribs.filter((k) => k !== key) : [...this.columns.attribs, key];
      this.saveColumnChoice();
      // A hidden column cannot stay the sort order.
      if (on && this.queryParams.orderBy === `attribs.${key}`) {
        this.querySubscribers({ orderBy: 'id', order: 'desc', page: 1 });
      }
    },

    toggleBaseColumn(id) {
      const hidden = this.columns.hidden.includes(id);
      this.columns.hidden = hidden ? this.columns.hidden.filter((c) => c !== id) : [...this.columns.hidden, id];
      this.saveColumnChoice();
    },

    addCustomColumn() {
      const key = this.customKeyClean;
      if (!key) return;
      if (!this.columns.attribs.includes(key)) this.toggleAttribColumn(key);
      this.customKey = '';
    },

    // Forgets the choice for this list and goes back to the suggested columns.
    resetColumns() {
      saveColumns(window.localStorage, this.columnsKey(), null);
      this.columns = {
        attribs: suggestColumns(this.columnStats), hidden: [], auto: true, suggested: this.columnStats.length > 0,
      };
      if (this.queryParams.orderBy.startsWith('attribs.')
        && !this.columns.attribs.includes(this.queryParams.orderBy.slice('attribs.'.length))) {
        this.querySubscribers({ orderBy: 'id', order: 'desc', page: 1 });
      }
    },

    onPickerToggle(active) {
      if (!active) this.customKey = '';
    },

    attribCell(row, key) {
      const text = attribText(row.attribs ? row.attribs[key] : undefined);
      return { text, long: text.length > LONG_VALUE };
    },

    isCellOpen(row, key) {
      return !!this.openCells[`${row.id}:${key}`];
    },

    toggleCell(row, key) {
      const id = `${row.id}:${key}`;
      this.$set(this.openCells, id, !this.openCells[id]);
    },

    // Tells whether the picked columns overflow the table's box (never on the mobile cards).
    checkTableWidth() {
      const wrapper = this.$refs.table && this.$refs.table.$el.querySelector('.table-wrapper');
      const wide = !!wrapper && wrapper.scrollWidth > wrapper.clientWidth + 1;
      if (wide !== this.isTableWide) this.isTableWide = wide;
    },

    hasFilter(key) {
      return this.attribFilters.some((f) => f.key === key);
    },

    chipText(f) {
      return this.$t(`subscribers.columns.chip.${f.op}`, { key: f.key, value: conditionValueText(f) });
    },

    // Opens the condition form, for one column when the header's filter button was used.
    openFilter(key) {
      if (this.attribFilters.length >= MAX_CONDITIONS) return;
      this.filterForm = newCondition(key);
      this.filterError = '';
      this.$nextTick(() => {
        const ref = key ? this.$refs.filterValue : this.$refs.filterKey;
        if (ref) ref.focus();
      });
    },

    closeFilter() {
      this.filterForm = null;
      this.filterError = '';
    },

    applyFilter() {
      const err = conditionError(this.filterForm);
      if (err) {
        this.filterError = this.$t(`lists.dynamic.${err}`);
        return;
      }
      this.attribFilters = [...this.attribFilters, cleanCondition(this.filterForm)];
      this.closeFilter();
      this.querySubscribers({ page: 1 });
    },

    removeFilter(index) {
      this.attribFilters = this.attribFilters.filter((_, i) => i !== index);
      this.querySubscribers({ page: 1 });
    },

    clearFilters() {
      this.attribFilters = [];
      this.querySubscribers({ page: 1 });
    },

    // Count the lists from which a subscriber has not unsubscribed.
    listCount(lists) {
      return lists.reduce((defVal, item) => (defVal + (item.subscriptionStatus !== 'unsubscribed' ? 1 : 0)), 0);
    },

    toggleAdvancedSearch() {
      this.isSearchAdvanced = !this.isSearchAdvanced;
      this.queryParams.search = '';

      // Toggling to simple search.
      if (!this.isSearchAdvanced) {
        this.queryInput = '';
        this.queryParams.queryExp = '';
        this.queryParams.page = 1;
        this.querySubscribers();
        this.$refs.query.focus();
        return;
      }

      // Toggling to advanced search.
      const q = this.queryInput.replace(/'/, "''").trim();
      if (q) {
        if (this.$utils.validateEmail(q)) {
          this.queryParams.queryExp = `email = '${q.toLowerCase()}'`;
        } else {
          this.queryParams.queryExp = `(name ~* '${q}' OR email ~* '${q.toLowerCase()}')`;
        }
      }

      // Toggling to advanced search.
      this.$nextTick(() => {
        this.$refs.queryExp.focus();
      });
    },

    // Mark all subscribers in the query as selected.
    selectAllSubscribers() {
      this.bulk.all = true;
    },

    onTableCheck() {
      // Disable bulk.all selection if there are no rows checked in the table.
      if (this.bulk.checked.length !== this.subscribers.total) {
        this.bulk.all = false;
      }
    },

    // Show the edit list form.
    showEditForm(sub) {
      this.curItem = sub;
      this.isFormVisible = true;
      this.isEditing = true;
    },

    // Show the new list form.
    showNewForm() {
      this.curItem = {};
      this.isFormVisible = true;
      this.isEditing = false;
    },

    showBulkListForm() {
      this.isBulkListFormVisible = true;
    },

    onFormClose() {
      if (this.$route.params.id) {
        this.$router.push({ name: 'subscribers' });
      }
    },

    onPageChange(p) {
      this.querySubscribers({ page: p });
    },

    onSort(field, direction) {
      this.querySubscribers({ orderBy: field, order: direction });
    },

    // Prepares an SQL expression for simple name search inputs and saves it
    // in this.queryExp.
    onSimpleQueryInput(v) {
      const q = v.replace(/'/, "''").trim();
      this.queryParams.queryExp = '';
      this.queryParams.page = 1;
      this.queryParams.search = q.toLowerCase();
    },

    // Ctrl + Enter on the advanced query searches.
    onAdvancedQueryEnter(e) {
      if (e.ctrlKey) {
        this.onSubmit();
      }
    },

    onSubmit() {
      this.querySubscribers({ page: 1 });
    },

    // Search / query subscribers.
    querySubscribers(params) {
      this.queryParams = { ...this.queryParams, ...params };

      const qp = {
        list_id: this.queryParams.listID,
        search: this.queryParams.search,
        query: this.queryParams.queryExp,
        page: this.queryParams.page,
        subscription_status: this.queryParams.subStatus,
        order_by: this.queryParams.orderBy,
        order: this.queryParams.order,
      };
      if (this.attribFilters.length) {
        qp.attrib_filter = JSON.stringify(this.attribFilters);
      }

      if (this.queryParams.queryExp) {
        delete qp.search;
      } else {
        delete qp.queryExp;
      }

      this.$nextTick(() => {
        this.$api.getSubscribers(qp).then(() => {
          this.bulk.checked = [];
        });
      });
    },

    deleteSubscriber(sub) {
      this.$utils.confirm(
        null,
        () => {
          this.$api.deleteSubscriber(sub.id).then(() => {
            this.querySubscribers();

            this.$utils.toast(this.$t('globals.messages.deleted', { name: sub.name }));
          });
        },
      );
    },

    blocklistSubscribers() {
      let fn = null;
      if (!this.bulk.all && this.bulk.checked.length > 0) {
        // If 'all' is not selected, blocklist subscribers by IDs.
        fn = () => {
          const ids = this.bulk.checked.map((s) => s.id);
          this.$api.blocklistSubscribers({ ids })
            .then(() => this.querySubscribers());
        };
      } else {
        // 'All' is selected, blocklist by query.
        fn = () => {
          this.$api.blocklistSubscribersByQuery({
            // Without any condition, `all=true` says that every subscriber of the list is meant.
            all: this.noConditions,
            attrib_filter: this.attribFilters,
            search: this.queryParams.search,
            query: this.queryParams.queryExp,
            list_ids: this.queryParams.listID ? [this.queryParams.listID] : null,
            subscription_status: this.queryParams.subStatus,
          }).then(() => this.querySubscribers());
        };
      }

      this.$utils.confirm(this.$t('subscribers.confirmBlocklist', { num: this.numSelectedSubscribers }), fn);
    },

    exportSubscribers() {
      const num = !this.bulk.all && this.bulk.checked.length > 0
        ? this.bulk.checked.length : this.subscribers.total;

      const cols = this.columns.attribs;
      const msg = this.$t('subscribers.confirmExport', { num })
        + (cols.length ? ` ${this.$t('subscribers.columns.exportNote', { columns: cols.join(', ') })}` : '');
      this.$utils.confirm(msg, () => {
        const q = new URLSearchParams();
        cols.forEach((key) => q.append('attrib_col', key));
        if (this.attribFilters.length) {
          q.append('attrib_filter', JSON.stringify(this.attribFilters));
        }

        if (this.queryParams.search) {
          q.append('search', this.queryParams.search);
        } else if (this.queryParams.queryExp) {
          q.append('query', this.queryParams.queryExp);
        }

        if (this.queryParams.listID) {
          q.append('list_id', this.queryParams.listID);
        }

        if (this.queryParams.subStatus) {
          q.append('subscription_status', this.queryParams.subStatus);
        }

        // Export selected subscribers.
        if (!this.bulk.all && this.bulk.checked.length > 0) {
          this.bulk.checked.map((s) => q.append('id', s.id));
        }

        document.location.href = `${uris.exportSubscribers}?${q.toString()}`;
      });
    },

    deleteSubscribers() {
      let fn = null;
      if (!this.bulk.all && this.bulk.checked.length > 0) {
        // If 'all' is not selected, delete subscribers by IDs.
        fn = () => {
          const ids = this.bulk.checked.map((s) => s.id);
          this.$api.deleteSubscribers({ id: ids })
            .then(() => {
              this.querySubscribers();

              this.$utils.toast(this.$t('subscribers.subscribersDeleted', { num: this.numSelectedSubscribers }));
            });
        };
      } else {
        // 'All' is selected, delete by query.
        fn = () => {
          this.$api.deleteSubscribersByQuery({
            // If the query expression is empty, explicitly pass `all=true`
            // so that the backend deletes all records in the DB with an empty query string.
            all: this.noConditions,
            attrib_filter: this.attribFilters,
            search: this.queryParams.search,
            query: this.queryParams.queryExp,
            list_ids: this.queryParams.listID ? [this.queryParams.listID] : null,
            subscription_status: this.queryParams.subStatus,
          }).then(() => {
            this.querySubscribers();

            this.$utils.toast(this.$t(
              'subscribers.subscribersDeleted',
              { num: this.numSelectedSubscribers },
            ));
          });
        };
      }

      this.$utils.confirm(this.$t('subscribers.confirmDelete', { num: this.numSelectedSubscribers }), fn);
    },

    bulkChangeLists(action, preconfirm, lists) {
      const data = {
        action,
        query: this.fullQueryExp,
        attrib_filter: this.attribFilters,
        search: this.queryParams.search,
        list_ids: this.queryParams.listID ? [this.queryParams.listID] : null,
        target_list_ids: lists.map((l) => l.id),
      };

      if (preconfirm) {
        data.status = 'confirmed';
      }

      let fn = null;
      if (!this.bulk.all && this.bulk.checked.length > 0) {
        // If 'all' is not selected, perform by IDs.
        fn = this.$api.addSubscribersToLists;
        data.ids = this.bulk.checked.map((s) => s.id);
      } else {
        // 'All' is selected, perform by query.
        data.query = this.queryParams.queryExp;
        data.subscription_status = this.queryParams.subStatus;
        fn = this.$api.addSubscribersToListsByQuery;
      }

      fn(data).then(() => {
        this.querySubscribers();
        this.$utils.toast(this.$t('subscribers.listChangeApplied'));
      });
    },
  },

  computed: {
    ...mapState(['subscribers', 'lists', 'loading']),

    // True when nothing narrows the list down: no search, no SQL query, no attribute condition.
    noConditions() {
      return this.queryParams.queryExp.trim() === '' && this.queryParams.search.trim() === ''
        && this.attribFilters.length === 0;
    },

    baseColumns() {
      const labels = {
        lists: this.$t('globals.terms.lists'),
        created_at: this.$t('globals.fields.createdAt'),
        updated_at: this.$t('globals.fields.updatedAt'),
      };
      return BASE_COLUMNS.map((id) => ({ id, label: labels[id] }));
    },

    // Keys offered in the column picker: those found on this page, most filled first, and
    // the picked ones this page has no value for.
    pickerKeys() {
      const found = [...this.columnStats].sort((a, b) => b.filled - a.filled);
      const known = new Set(found.map((s) => s.key));
      return [...found, ...this.columns.attribs.filter((k) => !known.has(k)).map((key) => ({ key, filled: 0 }))];
    },

    pageSize() {
      return (this.subscribers.results || []).length;
    },

    customKeyClean() {
      return cleanKey(this.customKey);
    },

    filterKeyOptions() {
      const typed = ((this.filterForm && this.filterForm.key) || '').toLowerCase();
      return this.pickerKeys.map((k) => k.key).filter((k) => k.toLowerCase().includes(typed));
    },

    numSelectedSubscribers() {
      if (this.bulk.all) {
        return this.subscribers.total;
      }
      return this.bulk.checked.length;
    },

    // Returns the list that the subscribers are being filtered by in.
    currentList() {
      if (!this.queryParams.listID || !this.lists.results) {
        return null;
      }

      return this.lists.results.find((l) => l.id === this.queryParams.listID);
    },
  },

  watch: {
    // The attribs come with the subscribers the table already loads.
    'subscribers.results': function onResults(results) {
      this.columnStats = attribStats(results);
      this.openCells = {};
      // Suggest columns once, from the first page that has subscribers, and keep them
      // while paging so that the table does not jump.
      if (this.columns.auto && !this.columns.suggested && (results || []).length) {
        this.columns = { ...this.columns, attribs: suggestColumns(this.columnStats), suggested: true };
      }
    },
  },

  created() {
    this.$root.$on('page.refresh', this.querySubscribers);
    window.addEventListener('resize', this.checkTableWidth);
  },

  updated() {
    this.$nextTick(this.checkTableWidth);
  },

  destroyed() {
    this.$root.$off('page.refresh', this.querySubscribers);
    window.removeEventListener('resize', this.checkTableWidth);
  },

  mounted() {
    if (this.$route.params.listID) {
      this.queryParams.listID = parseInt(this.$route.params.listID, 10);
    }
    if (this.$route.query.subscription_status) {
      this.queryParams.subStatus = this.$route.query.subscription_status;
    }

    const saved = loadColumns(window.localStorage, this.columnsKey());
    if (saved) {
      this.columns = { ...saved, auto: false, suggested: true };
    }

    if (this.$route.params.id) {
      this.$api.getSubscriber(parseInt(this.$route.params.id, 10)).then((data) => {
        this.showEditForm(data);
      });
    } else {
      // Get subscribers on load.
      this.querySubscribers();
    }
  },
});
</script>
