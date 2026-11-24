<template>
  <section class="import">
    <h1 class="title is-4">
      {{ $t('import.title') }}
    </h1>
    <b-loading :active="isLoading" />

    <section v-if="isFree()" class="wrap">
      <form @submit.prevent="onUpload" class="box">
        <div>
          <div class="columns">
            <div class="column">
              <b-field :label="$t('import.mode')" :addons="false">
                <div>
                  <b-radio v-model="form.mode" name="mode" native-value="subscribe" data-cy="check-subscribe">
                    {{ $t('import.subscribe') }}
                  </b-radio>
                  <br />
                  <b-radio v-model="form.mode" name="mode" native-value="blocklist" data-cy="check-blocklist">
                    {{ $t('import.blocklist') }}
                  </b-radio>
                </div>
              </b-field>
            </div>
            <div class="column">
              <b-field :label="$t('globals.fields.status')" :addons="false">
                <template v-if="form.mode === 'subscribe'">
                  <b-radio v-model="form.subStatus" name="subStatus" native-value="unconfirmed"
                    data-cy="check-unconfirmed">
                    {{ $t('subscribers.status.unconfirmed') }}
                  </b-radio>
                  <b-radio v-model="form.subStatus" name="subStatus" native-value="confirmed" data-cy="check-confirmed">
                    {{ $t('subscribers.status.confirmed') }}
                  </b-radio>
                </template>

                <b-radio v-else v-model="form.subStatus" name="subStatus" native-value="unsubscribed"
                  data-cy="check-unsubscribed">
                  {{ $t('subscribers.status.unsubscribed') }}
                </b-radio>
              </b-field>
            </div>

            <div class="column">
              <b-field :label="$t('import.csvDelim')" :message="$t('import.csvDelimHelp')" class="delimiter">
                <b-input v-model="form.delim" name="delim" placeholder="," maxlength="1" required />
              </b-field>
            </div>
          </div>

          <div class="columns">
            <div class="column is-4">
              <b-field v-if="form.mode === 'subscribe'" :label="$t('import.overwriteUserInfo')"
                :message="$can('lists:manage_all') ? $t('import.overwriteUserInfoHelp') : $t('teams.importOverwriteLocked')">
                <div>
                  <b-switch v-model="form.overwriteUserInfo" name="overwriteUserInfo" data-cy="overwrite-user-info"
                    :disabled="!$can('lists:manage_all')" />
                </div>
              </b-field>
            </div>

            <div class="column">
              <b-field v-if="form.mode === 'subscribe'" :label="$t('import.overwriteSubStatus')"
                :message="$t('import.overwriteSubStatusHelp')">
                <div>
                  <b-switch v-model="form.overwriteSubStatus" name="overwriteSubStatus"
                    data-cy="overwrite-sub-status" />
                </div>
              </b-field>
            </div>
          </div>

          <list-selector v-if="form.mode === 'subscribe'" :label="$t('globals.terms.lists')"
            :placeholder="$t('import.listSubHelp')" :message="$t('import.listSubHelp')" v-model="form.lists"
            :selected="form.lists" :all="(lists.results || []).filter(l => !l.axmFilter)" />
          <hr />

          <b-field :label="$t('import.csvFile')" label-position="on-border">
            <b-upload v-model="form.file" :disabled="isProcessing" drag-drop expanded>
              <div class="has-text-centered section">
                <p>
                  <b-icon icon="file-upload-outline" size="is-large" />
                </p>
                <p>{{ $t('import.csvFileHelp') }}</p>
              </div>
            </b-upload>
          </b-field>
          <div class="tags" v-if="form.file">
            <b-tag size="is-medium" :closable="!isProcessing" @close="clearFile">
              {{ form.file.name }}
            </b-tag>
          </div>
          <import-column-mapper v-if="isCSV" :mode="csv.mode" :encoding="csv.encoding" :encoding-choice="csv.encodingChoice"
            :raw-available="csv.rawReady && (!csv.busy || csv.parsed)"
            :header="csv.header" :header-hint="csv.headerHint" :columns="displayColumns" :source-columns="csv.columns"
            :samples="csv.samples"
            :mapping="csv.mapping" :column-errors="columnErrors" :column-warnings="columnWarnings"
            :existing-keys="existingKeys" :standard="standardAttribs" :values="csv.values"
            :validation="mappingMessages" :total="csv.total" :summary="csv.summary"
            :examples="csv.examples" :preview="csv.preview" :error="csv.error" :busy="csv.busy"
            :overwrite="form.mode === 'subscribe' && form.overwriteUserInfo" :address="addressView"
            @address-exclude="setAddressExclude" @address-download="downloadExcluded"
            @mapping="updateMapping" @mode="setCSVMode" @encoding="setEncoding" @header="setHeader" />
          <div class="buttons">
            <b-button native-type="submit" type="is-primary"
              :disabled="!canUpload || (form.mode === 'subscribe' && form.lists.length === 0)" :loading="isProcessing">
              {{ $t('import.upload') }}
            </b-button>
          </div>
        </div>
      </form>
      <br /><br />

      <div v-if="!isCSV || csv.mode === 'raw'" class="import-help">
        <h5 class="title is-size-6">
          {{ $t('import.instructions') }}
        </h5>
        <p>{{ $t('import.instructionsHelp') }}</p>
        <br />
        <blockquote class="csv-example">
          <code class="csv-headers"> <span>email,</span> <span>name,</span> <span>attributes</span></code>
        </blockquote>

        <hr />

        <h5 class="title is-size-6">
          {{ $t('import.csvExample') }}
        </h5>

        <pre class="csv-example" v-text="example" />
      </div>
    </section><!-- upload //-->

    <section v-if="isRunning() || isDone()" class="wrap status box has-text-centered">
      <b-progress :value="progress" show-value :type="countMismatch ? 'is-warning' : 'is-success'" />
      <br />
      <p
        class="is-size-5 is-capitalized"
        :class="[{ 'has-text-success': status.status === 'finished' && !countMismatch }, { 'has-text-danger': (status.status === 'failed' || status.status === 'stopped') }]">
        {{ status.status }}
      </p>

      <b-message v-if="countMismatch" type="is-warning" :closable="false" data-cy="map-mismatch" class="has-text-left">
        <strong>{{ $t('import.mapMismatchTitle') }}</strong>
        <p>{{ $t('import.mapMismatch', { sent: sentCount, imported: status.imported }) }}</p>
      </b-message>
      <p>{{ $t('import.recordsCount', { num: status.imported, total: status.total }) }}</p>
      <br />

      <b-message v-if="rejectedTotal" type="is-warning" :closable="false" class="has-text-left" data-cy="import-rejected">
        <strong>{{ $t('import.rejected.title', { count: rejectedTotal }) }}</strong>
        <table class="rejected-table">
          <tr v-for="r in rejectedLines" :key="r.reason" :data-cy="`import-rejected-${r.reason}`">
            <td>{{ $t(`import.rejected.reason.${r.reason}`) }}</td>
            <td class="has-text-right">{{ r.count }}</td>
          </tr>
        </table>
        <p>
          <a :href="`/api/import/subscribers/rejected?id=${status.axmImportId}`" download="rejected-rows.csv"
            data-cy="import-rejected-download">
            {{ $t('import.rejected.download') }}
          </a>
        </p>
      </b-message>

      <b-message v-if="warnedTotal" type="is-info" :closable="false" class="has-text-left" data-cy="import-warned">
        <strong>{{ $t('import.warned.title', { count: warnedTotal }) }}</strong>
        <table class="rejected-table">
          <tr v-for="r in warnedLines" :key="r.reason" :data-cy="`import-warned-${r.reason}`">
            <td>{{ $t(`import.warned.${r.reason}`) }}</td>
            <td class="has-text-right">{{ r.count }}</td>
          </tr>
        </table>
        <p class="help">{{ $t('import.warned.help') }}</p>
      </b-message>

      <p>
        <b-button @click="stopImport" :loading="isProcessing" icon-left="file-upload-outline" type="is-primary">
          {{ isDone() ? $t('import.importDone') : $t('import.stopImport') }}
        </b-button>
      </p>
      <br />

      <address-check v-if="showImportCheck" :list-ids="status.axmLists" :lists="lists.results || []"
        :hidden-lists="status.axmListsHidden || 0"
        :scope-note="$t('addressCheck.listScope', { lists: importListNames })" />

      <div class="import-logs">
        <log-view :lines="logs" :loading="false" />
      </div>
    </section>
  </section>
</template>

<script>
import Vue from 'vue';
import { mapState } from 'vuex';
import ListSelector from '../components/ListSelector.vue';
import LogView from '../components/LogView.vue';
import ImportColumnMapper from '../components/ImportColumnMapper.vue';
import AddressCheck from '../components/AddressCheck.vue';
import {
  LIMITS, readMemory, rememberMapping, addressClasses, addressExcludedRows, excludedRowsCSV,
} from '../lib/csv-import';

// While DNS lookups are unfinished the address check is asked again, this often and this many times.
const ADDRESS_POLL_MS = 3000;
const ADDRESS_MAX_POLLS = 10;

// What the address check leaves out of a new file until someone changes it: the confirmed
// risky rows, not the role and disposable ones.
function addressExcludeDefaults() {
  return { confirmed: true, warning: false };
}

function addressState() {
  return {
    state: 'idle', result: null, issues: Object.freeze({}), emails: Object.freeze({}), polls: 0,
  };
}

function csvState() {
  return {
    mode: 'mapping',
    modeChosen: false,
    encoding: '',
    encodingChoice: 'auto',
    header: true,
    columns: [],
    mapping: [],
    samples: [],
    // The columns whose assignment the user changed; suggestions never replace those.
    touched: [],
    // Per column saved under a standard item: how its values were converted.
    values: [],
    total: null,
    delimiter: '',
    headerHint: false,
    parsed: false,
    busy: false,
    ready: false,
    rawReady: false,
    limit: '',
    validation: [],
    summary: null,
    examples: {},
    preview: [],
    error: '',
  };
}

export default Vue.extend({
  components: {
    ListSelector,
    LogView,
    ImportColumnMapper,
    AddressCheck,
  },

  props: {
    data: { type: Object, default: () => { } },
    isEditing: { type: Boolean, default: false },
  },

  data() {
    return {
      form: {
        mode: 'subscribe',
        subStatus: 'unconfirmed',
        delim: ',',
        lists: [],
        overwriteUserInfo: false,
        overwriteSubStatus: false,
        file: null,
        example: '',
      },

      // Initial page load still has to wait for the status API to return
      // to either show the form or the status box.
      isLoading: true,

      csv: csvState(),
      csvRequest: 0,
      keyRequest: 0,
      existingKeys: [],
      // The address check of the rows to import, and the two switches of what it leaves out.
      addr: addressState(),
      addrExclude: addressExcludeDefaults(),
      sentCount: null,
      isProcessing: false,
      status: { status: '' },
      logs: [],
      pollID: null,
    };
  },

  watch: {
    'form.file': function fileChanged(file) {
      this.stopCSV();
      this.csv = csvState();
      // Every file starts with the default switches: what was chosen for an earlier file
      // must not leave rows out of (or let them into) this one without being seen.
      this.addrExclude = addressExcludeDefaults();
      if (file && /\.csv$/i.test(file.name)) {
        this.sentCount = null;
        this.parseCSV(true);
        this.loadAttributeKeys();
      }
    },
    'form.delim': function delimiterChanged(value) {
      if (!this.isCSV) return;
      if (!value) {
        this.stopCSV();
        this.csv.parsed = false;
        this.csv.delimiter = '';
        this.csv.ready = false;
        this.csv.busy = false;
        return;
      }
      if (value === this.csv.delimiter && this.csv.limit) return;
      if (value !== this.csv.delimiter || !this.csv.parsed) this.parseCSV(false, true);
    },
    'form.lists': function listsChanged() {
      if (this.isCSV) this.loadAttributeKeys();
    },
    'form.mode': function formMode() {
      // A blocklist import has no address check; coming back to subscribe runs it again.
      this.resetAddressCheck();
      if (this.form.mode === 'subscribe' && this.addrInput) this.checkAddresses(this.addrInput.emails, this.addrInput.rows);
      // Select the appropriate status radio whenever mode changes.
      this.$nextTick(() => {
        if (this.form.mode === 'subscribe') {
          this.form.subStatus = 'unconfirmed';
        } else {
          this.form.subStatus = 'unsubscribed';
        }
      });
    },
  },

  methods: {
    memory() {
      try {
        return readMemory(window.localStorage);
      } catch (_) {
        return readMemory();
      }
    },

    stopCSV() {
      this.csvRequest += 1;
      clearTimeout(this.csvTimer);
      if (this.csvWorker) this.csvWorker.terminate();
      this.csvWorker = null;
      this.csvBytes = null;
      this.csvSourceRows = null;
      this.csvKeysSent = '';
      this.addrInput = null;
      this.csvExclude = [];
      this.csvExcludeKey = '';
      this.resetAddressCheck(false);
    },

    // Forgets the address check. With apply, an upload built without the rows it left out is rebuilt.
    resetAddressCheck(apply = true) {
      clearTimeout(this.addrTimer);
      if (this.addrController) this.addrController.abort();
      this.addrKey = '';
      this.addr = addressState();
      if (apply) this.applyAddressExclusion();
    },

    // Checks the addresses of the rows to import. The same addresses are not asked about twice,
    // so rebuilding the upload without some rows does not start a new check.
    checkAddresses(emails, rows) {
      if (!emails) return;
      this.addrInput = { emails, rows };
      if (this.form.mode !== 'subscribe') return;
      // The rows are part of the key: the same addresses in other rows are another file to check.
      const key = `${emails.join('\n')}\n\n${rows.join(',')}`;
      if (key === this.addrKey) return;
      this.resetAddressCheck();
      this.addrKey = key;
      if (!emails.length) return;
      this.addr.state = 'checking';
      this.addrTimer = setTimeout(() => this.runAddressCheck(key), 400);
    },

    async runAddressCheck(key) {
      if (this.addrController) this.addrController.abort();
      const controller = new AbortController();
      this.addrController = controller;
      const { emails, rows } = this.addrInput;
      try {
        const result = await this.$api.checkImportAddresses({ emails }, controller.signal);
        if (controller.signal.aborted || key !== this.addrKey) return;
        const issues = {};
        const shown = {};
        const suggest = {};
        Object.entries(result.suggest || {}).forEach(([index, address]) => { suggest[rows[index]] = address; });
        Object.entries(result.issues || {}).forEach(([index, classes]) => {
          issues[rows[index]] = classes;
          shown[rows[index]] = emails[index];
        });
        const polls = this.addr.polls + (result.pending > 0 ? 1 : 0);
        this.addr = {
          state: 'done', result, issues: Object.freeze(issues), emails: Object.freeze(shown), suggest: Object.freeze(suggest), polls, gate: result.gate || '',
        };
        this.applyAddressExclusion();
        if (result.pending > 0 && polls <= ADDRESS_MAX_POLLS) {
          this.addrTimer = setTimeout(() => this.runAddressCheck(key), ADDRESS_POLL_MS);
        }
      } catch (_) {
        // A failed check never blocks the upload: without an answer it is an error, and with
        // one the unfinished lookups are given up on.
        if (controller.signal.aborted || key !== this.addrKey) return;
        if (this.addr.state === 'done') this.addr = { ...this.addr, polls: ADDRESS_MAX_POLLS + 1 };
        else this.addr.state = 'error';
      }
    },

    // Rebuilds the upload when the rows the address check leaves out have changed.
    applyAddressExclusion() {
      const rows = addressExcludedRows(this.addr.issues, { ...this.addrExclude, gate: this.addr.gate });
      const key = rows.join(',');
      if (key === (this.csvExcludeKey || '')) return;
      this.csvExclude = rows;
      this.csvExcludeKey = key;
      if (this.csv.parsed && this.csv.mode === 'mapping') this.convertCSV(true);
    },

    setAddressExclude(patch) {
      this.addrExclude = { ...this.addrExclude, ...patch };
      this.applyAddressExclusion();
    },

    // The rows the address check left out, with why: made here and never sent anywhere.
    // The rows the column mapping leaves out for their address, with the reason the box shows:
    // the address check's confirmed rows and the rows that are not an address at all.
    excludedForServer() {
      const out = (this.csvExclude || []).map((row) => ({
        row,
        email: this.addr.emails[row] || '',
        reason: addressClasses(this.addr.gate).confirmed.find((c) => (this.addr.issues[row] || []).includes(c)),
        suggest: (this.addr.suggest || {})[row] || '',
      })).filter((x) => x.reason);
      (this.csvInvalidRows || []).forEach(({ row, email }) => out.push({
        row, email, reason: 'format', suggest: '',
      }));
      return out.slice(0, LIMITS.rows);
    },

    // The rows the review leaves out for their address, for the download on the review screen:
    // the address check's rows and the rows that are not an address at all, by source row. The
    // same rows the box that ends the import counts.
    excludedRows() {
      const classes = addressClasses(this.addr.gate);
      const reasons = [...classes.confirmed, ...classes.warning];
      const format = [this.$t('addressCheck.class.format')];
      return [
        ...(this.csvExclude || []).map((row) => ({
          row,
          email: this.addr.emails[row] || '',
          // The reason of the server is the first of the classes it refuses; a row it takes in
          // shows its warnings.
          reasons: this.excludedReasons(row, classes, reasons).map((c) => this.$t(`addressCheck.class.${c}`)),
          suggest: (this.addr.suggest || {})[row] || '',
        })),
        ...(this.csvInvalidRows || []).map(({ row, email }) => ({
          row, email, reasons: format, suggest: '',
        })),
      ].sort((a, b) => a.row - b.row);
    },

    excludedReasons(row, classes, all) {
      const found = this.addr.issues[row] || [];
      const first = classes.confirmed.find((c) => found.includes(c));
      return first ? [first] : found.filter((c) => all.includes(c));
    },

    saveCSV(csv, name) {
      const link = document.createElement('a');
      link.href = URL.createObjectURL(new Blob(['\uFEFF', csv], { type: 'text/csv;charset=utf-8' }));
      link.download = name;
      document.body.appendChild(link);
      link.click();
      link.remove();
      setTimeout(() => URL.revokeObjectURL(link.href), 1000);
    },

    downloadExcluded() {
      this.saveCSV(
        excludedRowsCSV(this.excludedRows(), [this.$t('import.addr.csvRow'), this.$t('import.addr.csvEmail'), this.$t('import.addr.csvReason'), this.$t('import.rejected.csvSuggest')]),
        `${this.form.file.name.replace(/\.csv$/i, '')}-excluded.csv`,
      );
    },

    async parseCSV(initial = false, manualDelimiter = false) {
      const { file } = this.form;
      if (!this.isCSV) return;
      this.stopCSV();
      const id = this.csvRequest;
      this.csv.busy = true;
      this.csv.ready = false;
      this.csv.rawReady = false;
      this.csv.limit = '';
      this.csv.encoding = '';
      this.csv.total = null;
      this.csv.parsed = false;
      this.csv.error = '';
      this.csv.columns = [];
      this.csv.summary = null;
      this.csv.preview = [];
      this.csv.values = [];
      this.csv.touched = [];
      this.csv.validation = [];
      try {
        const oversized = file.size > LIMITS.input;
        const bytes = oversized ? null : await file.arrayBuffer();
        if (id !== this.csvRequest) return;
        // Vite emits this module under /admin/static in packed builds.
        this.csvWorker = new Worker(new URL('../lib/csv-import.worker.js', import.meta.url), { type: 'module' });
        const worker = this.csvWorker;
        this.csvWorker.onerror = () => {
          if (worker === this.csvWorker) this.csvFailure('worker');
        };
        this.csvWorker.onmessage = ({ data }) => {
          if (data.type === 'suggest') {
            // Only this file's Worker can answer; columns the user set stay as they are.
            if (worker !== this.csvWorker || !this.csv.parsed || data.mapping.length !== this.csv.mapping.length) return;
            const next = this.csv.mapping.map((entry, n) => (this.csv.touched.includes(n) ? entry : data.mapping[n]));
            if (JSON.stringify(next) !== JSON.stringify(this.csv.mapping)) this.csv.mapping = next;
            this.convertCSV();
            return;
          }
          if (data.id !== this.csvRequest) return;
          if (data.type === 'error') {
            this.csvFailure(data.code, data.total);
          } else if (data.type === 'source') {
            this.csv.encoding = data.encoding;
            this.csv.rawReady = data.encoding === 'utf-8';
            this.csv.delimiter = data.delimiter;
            if (initial || !this.csv.modeChosen) {
              this.csv.mode = data.standard && this.csv.rawReady ? 'raw' : 'mapping';
              this.csv.modeChosen = true;
            }
            if (!this.csv.rawReady) this.csv.mode = 'mapping';
          } else if (data.type === 'parse') {
            Object.assign(this.csv, {
              encoding: data.encoding,
              delimiter: data.delimiter,
              columns: data.columns,
              total: data.total,
              headerHint: data.headerHint,
              mapping: data.mapping,
              samples: data.samples,
              parsed: true,
            });
            this.form.delim = data.delimiter;
            // The names in use may have arrived while the file was being read.
            if (this.csvKeysSent !== JSON.stringify(this.existingKeys)) this.suggestAgain();
            else this.convertCSV();
          } else if (data.type === 'convert') {
            this.csvBytes = data.bytes;
            this.csvSourceRows = data.sourceRows;
            this.csvInvalidRows = data.invalidRows || [];
            Object.assign(this.csv, {
              busy: false,
              ready: !!data.bytes && data.summary.imported > 0,
              validation: data.validation,
              summary: data.summary || null,
              examples: data.examples || {},
              preview: data.preview || [],
              // A reply without a conversion (another column is wrong) keeps the last counts.
              values: data.values || this.csv.values,
            });
            this.checkAddresses(data.checkEmails, data.checkRows);
          }
        };
        const delimiter = initial || (!manualDelimiter && !this.csv.delimiter) ? '' : this.form.delim;
        if (oversized) {
          if (this.csv.encodingChoice === 'cp949') {
            this.csv.mode = 'mapping';
            this.csvFailure('inputLimit');
            return;
          }
          this.csvWorker.postMessage({
            id, type: 'inspectOriginal', file, delimiter,
          });
          return;
        }
        this.csvKeysSent = JSON.stringify(this.existingKeys);
        this.csvWorker.postMessage({
          id,
          type: 'parse',
          bytes,
          encoding: this.csv.encodingChoice,
          delimiter,
          header: this.csv.header,
          memory: this.memory(),
          standard: this.standardAttribs,
          keys: this.existingKeys,
        }, [bytes]);
      } catch (error) {
        if (id === this.csvRequest) this.csvFailure(error.code || 'read');
      }
    },

    csvFailure(code, total) {
      this.csv.busy = false;
      this.csv.ready = false;
      this.csvBytes = null;
      if (total !== undefined) this.csv.total = total;
      if (['inputLimit', 'rowLimit'].includes(code)) {
        this.csv.limit = code;
        if (this.csv.delimiter) this.form.delim = this.csv.delimiter;
        if (this.csv.mode === 'raw' && this.csv.rawReady) {
          this.csv.error = '';
          return;
        }
      } else if (code !== 'outputLimit') {
        this.csv.rawReady = false;
      }
      this.csv.error = this.$t(`import.mapError${code}`);
    },

    // keep leaves the review on screen while the upload is rebuilt (the address check changed
    // only which rows are left out).
    convertCSV(keep = false) {
      clearTimeout(this.csvTimer);
      this.csvRequest += 1;
      this.csvBytes = null;
      this.csv.ready = false;
      if (!keep) {
        this.csv.summary = null;
        this.csv.preview = [];
      }
      if (this.csv.limit) {
        this.csv.error = this.csv.mode === 'mapping' ? this.$t(`import.mapError${this.csv.limit}`) : '';
        this.csv.busy = false;
        return;
      }
      this.csv.error = '';
      if (!this.csv.parsed) return;
      if (this.csv.mode === 'raw') {
        this.csv.busy = false;
        return;
      }
      this.csv.busy = true;
      this.csvTimer = setTimeout(() => {
        if (this.csvWorker) {
          this.csvWorker.postMessage({
            id: this.csvRequest, type: 'convert', mapping: this.csv.mapping, keys: this.existingKeys, standard: this.standardAttribs, exclude: this.csvExclude || [],
          });
        }
      }, 100);
    },

    updateMapping(mapping) {
      mapping.forEach((entry, n) => {
        const old = this.csv.mapping[n] || {};
        if ((entry.role !== old.role || entry.key !== old.key) && !this.csv.touched.includes(n)) this.csv.touched.push(n);
      });
      this.csv.mapping = mapping;
      this.convertCSV();
    },
    setCSVMode(mode) {
      if (!this.csv.parsed && !this.csv.limit) return;
      if (mode === 'raw' && !this.csv.rawReady) return;
      this.csv.mode = mode;
      this.csv.modeChosen = true;
      this.convertCSV();
    },
    setEncoding(value) {
      this.csv.encodingChoice = value;
      this.parseCSV();
    },
    setHeader(value) {
      this.csv.header = value;
      this.parseCSV();
    },

    async loadAttributeKeys() {
      this.keyRequest += 1;
      const request = this.keyRequest;
      const keys = new Set(this.memory().keys);
      this.existingKeys = [...keys];
      const results = await Promise.all(this.form.lists.slice(0, 5).map((list) => this.$api.getSubscriberAttribSample({
        list_id: list.id, page: 1, per_page: 50, order_by: 'id', order: 'desc',
      }).catch(() => null)));
      if (request !== this.keyRequest || !this.isCSV) return;
      results.forEach((result) => (result?.results || []).forEach((subscriber) => {
        Object.keys(subscriber.attribs || {}).forEach((key) => keys.add(key));
      }));
      this.existingKeys = [...keys].sort();
      if (this.csv.parsed) this.suggestAgain();
    },

    // A name already in use may fit a column better than the one made up without it. The
    // Worker suggests again with the current names; the reply keeps the columns the user set.
    suggestAgain() {
      if (!this.csvWorker || this.csv.limit) {
        this.convertCSV();
        return;
      }
      this.csvKeysSent = JSON.stringify(this.existingKeys);
      this.csvWorker.postMessage({
        type: 'suggest', memory: this.memory(), standard: this.standardAttribs, keys: this.existingKeys,
      });
    },

    clearFile() {
      this.form.file = null;
    },

    // Returns true if we're free to do an upload.
    isFree() {
      if (this.status.status === 'none') {
        return true;
      }
      return false;
    },

    // Returns true if an import is running.
    isRunning() {
      if (this.status.status === 'importing'
        || this.status.status === 'stopping') {
        return true;
      }
      return false;
    },

    isSuccessful() {
      return this.status.status === 'finished';
    },

    isFailed() {
      return (
        this.status.status === 'stopped'
        || this.status.status === 'failed'
      );
    },

    // Returns true if an import has finished (failed or successful).
    isDone() {
      if (this.status.status === 'finished'
        || this.status.status === 'stopped'
        || this.status.status === 'failed'
      ) {
        return true;
      }
      return false;
    },

    pollStatus() {
      // Clear any running status polls.
      clearInterval(this.pollID);

      // Poll for the status as long as the import is running.
      this.pollID = setInterval(() => {
        this.$api.getImportStatus().then((data) => {
          this.isProcessing = false;
          this.isLoading = false;
          this.status = data;
          this.getLogs();

          if (!this.isRunning()) {
            clearInterval(this.pollID);
          }
        }, () => {
          this.isProcessing = false;
          this.isLoading = false;
          this.status = { status: 'none' };
          clearInterval(this.pollID);
        });
        return true;
      }, 250);
    },

    getLogs() {
      this.$api.getImportLogs().then((data) => {
        this.logs = data.split('\n').map((line) => line.replace(/\s+importer\.go:\d+:\s*/, ' *: '));
        Vue.nextTick(() => {
          // vue.$refs doesn't work as the logs textarea is rendered dynamically.
          const ref = document.getElementById('import-log');
          if (ref) {
            ref.scrollTop = ref.scrollHeight;
          }
        });
      });
    },

    // Cancel a running import or clears a finished import.
    stopImport() {
      this.isProcessing = true;
      this.$api.stopImport().then(() => {
        this.pollStatus();
        this.form.file = null;
        this.sentCount = null;
      });
    },

    renderExample() {
      const h = 'email,name,attributes\n'
        + 'user1@mail.com,"User One","{""age"": 42, ""planet"": ""Mars""}"\n'
        + 'user2@mail.com,"User Two","{""age"": 24, ""job"": ""Time Traveller""}"';

      this.example = h;
    },

    resetForm() {
      this.form.mode = 'subscribe';
      this.form.overwriteUserInfo = false;
      this.form.overwriteSubStatus = false;
      this.form.file = null;
      this.form.lists = [];
      this.form.subStatus = 'unconfirmed';
      this.form.delim = ',';
    },

    onUpload() {
      if (this.form.mode === 'subscribe' && this.form.overwriteSubStatus) {
        this.$utils.confirm(this.$t('import.subscribeWarning'), this.onSubmit, this.resetForm);
        return;
      }

      this.onSubmit();
    },

    onSubmit() {
      if (!this.canUpload) return;
      const mapped = this.isCSV && this.csv.mode === 'mapping';
      const file = mapped ? new File([this.csvBytes], `${this.form.file.name.replace(/\.csv$/i, '')}.csv`, { type: 'text/csv' }) : this.form.file;
      this.sentCount = mapped ? this.csv.summary.imported : null;
      if (mapped && !this.csv.headerHint) {
        try {
          rememberMapping(window.localStorage, this.csv.columns, this.csv.mapping);
        } catch (_) { /* Remembering assignments is optional. */ }
      }
      this.isProcessing = true;

      // Prepare the upload payload.
      const params = new FormData();
      params.set('params', JSON.stringify({
        mode: this.form.mode,
        subscription_status: this.form.subStatus,
        delim: mapped ? ',' : this.form.delim,
        lists: this.form.lists.map((l) => l.id),
        overwrite_userinfo: this.form.overwriteUserInfo,
        overwrite_subscription_status: this.form.overwriteSubStatus,
        // The rows the column mapping left out: the server counts them with its own refusals.
        axm_excluded: mapped && this.form.mode === 'subscribe' ? this.excludedForServer() : [],
        // The row of the chosen file each uploaded row came from, for the numbers in the rejected rows.
        axm_source_rows: mapped ? (this.csvSourceRows || []) : [],
        // The records of the chosen file, for the total the end of the import shows.
        axm_file_rows: mapped ? (this.csv.total || 0) : 0,
      }));
      params.set('file', file);

      // Post.
      this.$api.importSubscribers(params).then(() => {
        // On file upload, show a confirmation.
        this.$utils.toast(this.$t('import.importStarted'));

        // Start polling status.
        this.pollStatus();
      }, () => {
        this.isProcessing = false;
        this.form.file = null;
        this.pollStatus();
      });
    },
  },

  computed: {
    ...mapState(['lists', 'serverConfig']),
    // The standard attribute dictionary: [{ key, kind, aliases }].
    standardAttribs() {
      return this.serverConfig.axm_standard_attribs || [];
    },
    isCSV() {
      return !!this.form.file && /\.csv$/i.test(this.form.file.name);
    },
    canUpload() {
      if (!this.form.file || this.isProcessing || this.form.delim.length !== 1) return false;
      if (!this.isCSV) return true;
      if (this.csv.busy || this.csv.error) return false;
      // Uploading before the check answers would let the rows it leaves out through.
      if (this.csv.mode === 'mapping' && this.addressBusy) return false;
      return this.csv.mode === 'raw' ? this.csv.rawReady : this.csv.parsed && this.csv.ready;
    },
    displayColumns() {
      return this.csv.columns.map((column, index) => (this.csv.header && column ? column : this.$t('import.mapUnnamed', { num: index + 1 })));
    },
    // Problems that belong to one column are shown in that column's row.
    mappingMessages() {
      return this.csv.validation.filter(({ index }) => index === undefined).map(({ type }) => this.$t(`import.mapValidation${type}`));
    },
    columnErrors() {
      const errors = {};
      this.csv.validation.forEach(({ type, index }) => {
        // Values that cannot be read are shown by the mapper itself, with what to do about them.
        if (type === 'unreadable') return;
        if (index !== undefined) {
          const detail = type === 'key' && this.csv.mapping[index].key ? 'keyInvalid' : type;
          errors[index] = this.$t(`import.mapColumnError${detail}`);
        } else {
          this.csv.mapping.forEach(({ role }, n) => {
            if ((type === 'emailColumns' && role === 'email') || (type === 'nameColumns' && role === 'name')) errors[n] = this.$t(`import.mapColumnError${type}`);
          });
        }
      });
      return errors;
    },
    // Rows that will be left out do not block the upload.
    columnWarnings() {
      const count = this.csv.summary ? this.csv.summary.empty + this.csv.summary.invalid : 0;
      return count ? { [this.csv.mapping.findIndex(({ role }) => role === 'email')]: this.$t('import.mapEmailErrors', { count }) } : {};
    },
    addressPolling() {
      const { state, result, polls } = this.addr;
      return state === 'done' && result.pending > 0 && polls <= ADDRESS_MAX_POLLS;
    },
    // The check has not answered for every row yet (first answer, or lookups still running).
    addressBusy() {
      return this.form.mode === 'subscribe' && (this.addr.state === 'checking' || this.addressPolling);
    },
    // The finished box checks the lists the server says this import subscribed people to (it
    // sends them with the status, after its own permission filter; none for a blocklist import).
    showImportCheck() {
      return this.isSuccessful() && (this.status.axmLists || []).length > 0;
    },
    importListNames() {
      const all = this.lists.results || [];
      return (this.status.axmLists || []).map((id) => (all.find((l) => l.id === id) || { name: `#${id}` }).name).join(', ');
    },
    // What the column mapper shows of the address check: counts, one line per class with the
    // first source rows, and how many rows each switch leaves out.
    addressView() {
      const {
        state, result, issues, emails,
      } = this.addr;
      const suggest = this.addr.suggest || {};
      if (state === 'idle' || this.form.mode !== 'subscribe') return null;
      if (state !== 'done') return { state, busy: this.addressBusy };
      const rows = Object.keys(issues).map(Number).sort((a, b) => a - b);
      const has = (row, classes) => issues[row].some((c) => classes.includes(c));
      const classes = addressClasses(this.addr.gate);
      const confirmed = rows.filter((row) => has(row, classes.confirmed));
      const confirmedSet = new Set(confirmed);
      const warningOnly = rows.filter((row) => !confirmedSet.has(row) && has(row, classes.warning));
      // A row the server refuses is shown under its refusal only: it is not also "imported with
      // a warning" or "imported as it is".
      const open = rows.filter((row) => !confirmedSet.has(row));
      const unknownRows = open.filter((row) => issues[row].includes('unknown'));
      const pendingRows = open.filter((row) => issues[row].includes('pending'));
      const polling = this.addressPolling;
      // A misspelled domain is shown with the address it should be.
      const shownAddress = (row) => (suggest[row] ? `${emails[row]} → ${suggest[row]}` : emails[row]);
      const lines = [...classes.confirmed, ...classes.warning, 'unknown'].map((name) => {
        const inLine = (row) => issues[row].includes(name) && (classes.confirmed.includes(name) || !confirmedSet.has(row));
        const found = rows.filter(inLine);
        let kind = 'unknown';
        if (classes.confirmed.includes(name)) kind = 'confirmed';
        else if (classes.warning.includes(name)) kind = 'warning';
        let help = '';
        if (['provider', 'typo'].includes(name)) help = this.$t(`addressCheck.classHelp.${name}`);
        else if (['suppressed', 'bounced', 'disposable', 'dead', 'nullmx'].includes(name) && kind === 'confirmed') help = this.$t(`import.addr.help.${name}`);
        else if (name === 'role') help = this.$t('import.addr.help.role');
        else if (name === 'unknown') help = this.$t('import.addr.unknownHelp');
        return {
          name,
          kind,
          help,
          count: found.length,
          examples: `${found.slice(0, 5).map((row) => `${row} (${shownAddress(row)})`).join(', ')}${found.length > 5 ? ' …' : ''}`,
        };
      }).filter(({ count }) => count);
      return {
        state,
        lines,
        total: result.total,
        // Counted here from the rows: the server's summary counts a disposable domain as a
        // warning and knows nothing of the blocklist and bounce classes (LST-56).
        risky: confirmed.length + warningOnly.length,
        confirmed: confirmed.length,
        warning: warningOnly.length,
        pending: polling ? pendingRows.length : 0,
        unknown: unknownRows.length + (polling ? 0 : pendingRows.length),
        busy: this.addressBusy,
        confirmedRows: confirmed.length,
        // What the second switch adds: with the first one on, rows it already leaves out are not counted again.
        warningRows: rows.filter((row) => has(row, classes.warning) && !(this.addrExclude.confirmed && confirmedSet.has(row))).length,
        excludeConfirmed: this.addrExclude.confirmed,
        excludeWarning: this.addrExclude.warning,
        // With the gate at basic a disposable domain is a warning row too.
        warningKey: this.addr.gate === 'basic' ? 'import.addr.excludeWarningBasic' : 'import.addr.excludeWarning',
        // What the server looked at: with the gate at nodns or basic it did not look domains up
        // (and with basic not the blocklist and bounces either), so "no risky addresses" says so.
        sourceKey: ({ nodns: 'import.addr.serverNoDns', basic: 'import.addr.serverBasic' })[this.addr.gate] || 'import.addr.server',
        cleanKey: ({ nodns: 'import.addr.cleanNoDns', basic: 'import.addr.cleanBasic' })[this.addr.gate] || 'import.addr.clean',
      };
    },
    // Rows the server refused because of the address rules (LST-53): counted by reason.
    // The server's refusals and the rows the column mapping left out (sent with the upload).
    rejectedTotal() {
      return (this.status && this.status.axmRejectedTotal) || 0;
    },
    // Rows the server itself refused: the ones the upload carried count as sent, not as missing.
    serverRejected() {
      return this.rejectedTotal - ((this.status && this.status.axmExcluded) || 0);
    },
    rejectedLines() {
      const counts = (this.status && this.status.axmRejected) || {};
      return ['suppressed', 'bounced', 'disposable', 'nullmx', 'dead', 'typo', 'provider', 'format', 'domain']
        .filter((r) => counts[r]).map((r) => ({ reason: r, count: counts[r] }));
    },
    // Rows that came in with a warning (LST-56): the server imported them and only counts them.
    warnedTotal() {
      return (this.status && this.status.axmWarnedTotal) || 0;
    },
    warnedLines() {
      const counts = (this.status && this.status.axmWarned) || {};
      return ['unknown', 'role'].filter((r) => counts[r]).map((r) => ({ reason: r, count: counts[r] }));
    },
    countMismatch() {
      return this.sentCount !== null && this.isDone() && this.status.imported + this.serverRejected !== this.sentCount;
    },

    // Import progress bar value.
    progress() {
      if (!this.status || !this.status.total > 0) {
        return 0;
      }
      return Math.ceil((this.status.imported / this.status.total) * 100);
    },
  },

  beforeDestroy() {
    clearInterval(this.pollID);
    clearTimeout(this.addrTimer);
    this.stopCSV();
    this.keyRequest += 1;
  },

  mounted() {
    this.renderExample();
    this.pollStatus();

    const ids = this.$utils.parseQueryIDs(this.$route.query.list_id);
    if (ids.length > 0 && this.lists.results) {
      this.$nextTick(() => {
        this.form.lists = this.lists.results.filter((l) => !l.axmFilter && ids.indexOf(l.id) > -1);
      });
    }
  },
});
</script>

<style scoped>
.rejected-table { width: 100%; margin: 0.4rem 0; border-collapse: collapse; }
.rejected-table td { padding: 0.15rem 0.25rem; border-top: 1px solid rgba(97, 70, 0, 0.2); }
</style>
