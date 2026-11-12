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
                :message="$t('import.overwriteUserInfoHelp')">
                <div>
                  <b-switch v-model="form.overwriteUserInfo" name="overwriteUserInfo" data-cy="overwrite-user-info" />
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
            :existing-keys="existingKeys" :validation="mappingMessages" :total="csv.total" :summary="csv.summary"
            :examples="csv.examples" :preview="csv.preview" :error="csv.error" :busy="csv.busy"
            :overwrite="form.mode === 'subscribe' && form.overwriteUserInfo"
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

      <p>
        <b-button @click="stopImport" :loading="isProcessing" icon-left="file-upload-outline" type="is-primary">
          {{ isDone() ? $t('import.importDone') : $t('import.stopImport') }}
        </b-button>
      </p>
      <br />

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
import { LIMITS, readMemory, rememberMapping } from '../lib/csv-import';

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
            this.convertCSV();
          } else if (data.type === 'convert') {
            this.csvBytes = data.bytes;
            this.csvSourceRows = data.sourceRows;
            Object.assign(this.csv, {
              busy: false,
              ready: !!data.bytes && data.summary.imported > 0,
              validation: data.validation,
              summary: data.summary || null,
              examples: data.examples || {},
              preview: data.preview || [],
            });
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
        this.csvWorker.postMessage({
          id,
          type: 'parse',
          bytes,
          encoding: this.csv.encodingChoice,
          delimiter,
          header: this.csv.header,
          memory: this.memory(),
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

    convertCSV() {
      clearTimeout(this.csvTimer);
      this.csvRequest += 1;
      this.csvBytes = null;
      this.csv.ready = false;
      this.csv.summary = null;
      this.csv.preview = [];
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
            id: this.csvRequest, type: 'convert', mapping: this.csv.mapping, keys: this.existingKeys,
          });
        }
      }, 100);
    },

    updateMapping(mapping) {
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
      if (this.csv.parsed) this.convertCSV();
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
    ...mapState(['lists']),
    isCSV() {
      return !!this.form.file && /\.csv$/i.test(this.form.file.name);
    },
    canUpload() {
      if (!this.form.file || this.isProcessing || this.form.delim.length !== 1) return false;
      if (!this.isCSV) return true;
      if (this.csv.busy || this.csv.error) return false;
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
    countMismatch() {
      return this.sentCount !== null && this.isDone() && this.status.imported !== this.sentCount;
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
