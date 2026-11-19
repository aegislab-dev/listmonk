<template>
  <div class="box tracking-check" data-cy="tracking-check" :data-state="state">
    <div class="tc-head">
      <h3 class="title is-size-6">{{ $t('campaigns.tracking.title') }}</h3>
      <span class="tag" :class="tag.type" data-cy="tracking-tag" role="status">{{ $t(tag.key) }}</span>
    </div>

    <div v-if="error && !pending" class="notification is-danger is-light" role="alert" data-cy="tracking-error">
      {{ $t('campaigns.tracking.error', { error }) }}
    </div>
    <div v-if="report && report.state === 'off'" class="notification is-danger is-light" data-cy="tracking-off">
      {{ $t('campaigns.tracking.offNotice') }}
    </div>

    <ul v-if="rows.length" class="tc-rows">
      <li v-for="row in rows" :key="row.name" class="tc-row" :class="`is-${row.level}`" :data-cy="`tracking-${row.name}`">
        <b-icon :icon="icons[row.level]" size="is-small" />
        <div>
          <p>
            <strong>{{ $t(row.label) }}</strong><span class="tc-state">{{ pending ? '…' : $t(row.state, row.stateParams) }}</span>
          </p>
          <template v-if="!pending">
            <p v-if="row.help" class="help">
              <template v-for="(part, i) in codeParts($t(row.help, row.helpParams))">
                <code v-if="i % 2" :key="i">{{ part }}</code><template v-else>{{ part }}</template>
              </template>
            </p>
            <template v-if="row.name === 'click' && report.links.untracked > 0">
              <p class="help">
                <a href="#" data-cy="tracking-links-toggle" :aria-expanded="open ? 'true' : 'false'" @click.prevent="open = !open">
                  {{ $t(open ? 'campaigns.tracking.click.hideLinks' : 'campaigns.tracking.click.showLinks', { n: report.links.untracked }) }}
                </a>
              </p>
              <div v-if="open" class="tc-links" data-cy="tracking-links">
                <div v-for="u in report.links.untrackedUrls" :key="u">{{ u }}</div>
                <div v-if="report.links.untracked > report.links.untrackedUrls.length" class="has-text-grey">
                  {{ $t('campaigns.tracking.click.moreLinks', { n: report.links.untracked - report.links.untrackedUrls.length }) }}
                </div>
              </div>
            </template>
          </template>
        </div>
      </li>
    </ul>

    <div class="tc-foot">
      <p class="help">{{ $t('campaigns.tracking.help') }} {{ $t('campaigns.tracking.testHelp') }}</p>
      <a href="#" data-cy="tracking-recheck" @click.prevent="run"><b-icon icon="refresh" size="is-small" /> {{ $t('campaigns.tracking.recheck') }}</a>
    </div>
  </div>
</template>

<script>
import { codeParts, trackingRows, trackingTag } from '../tracking-check';

const DEBOUNCE = 800;
// Rows shown while the first check is on its way.
const PLACEHOLDER = {
  state: '', individual: true, view: 'manual', links: { total: 0, untracked: 0 },
};

export default {
  props: {
    campaignId: { type: Number, required: true },
    // The editor's content: { contentType, body, templateId }.
    content: { type: Object, required: true },
    messenger: { type: String, default: 'email' },
  },

  data() {
    return {
      report: null,
      error: '',
      pending: true,
      open: false,
      timer: null,
      seq: 0,
      inflight: null,
      icons: {
        ok: 'check-circle-outline', warn: 'warning-empty', na: 'minus', off: 'cancel',
      },
    };
  },

  computed: {
    payload() {
      return {
        content_type: this.content.contentType,
        body: this.content.body || '',
        template_id: this.content.templateId || 0,
        messenger: this.messenger,
      };
    },
    key() { return JSON.stringify(this.payload); },
    state() {
      if (this.pending) return 'checking';
      return this.report ? this.report.state : 'failed';
    },
    tag() {
      return this.pending ? { key: 'campaigns.tracking.checking', type: 'is-light' } : trackingTag(this.report);
    },
    rows() {
      if (this.pending) {
        return trackingRows(this.report || PLACEHOLDER).filter((r) => r.name !== 'individual').map((r) => ({ ...r, level: 'na' }));
      }
      return trackingRows(this.report);
    },
  },

  watch: {
    key: 'schedule',
    campaignId: 'schedule',
  },

  mounted() { this.run(); },

  beforeDestroy() {
    clearTimeout(this.timer);
    this.seq += 1;
  },

  methods: {
    codeParts,

    // An edit: check again once the typing pauses.
    schedule() {
      this.pending = true;
      clearTimeout(this.timer);
      this.timer = setTimeout(() => this.run(), DEBOUNCE);
    },

    // Checks the content as it is now. Resolves with the report, or null when the
    // check could not be made. A newer check makes an older answer void.
    run() {
      clearTimeout(this.timer);
      this.timer = null;
      this.seq += 1;
      const { seq } = this;
      this.pending = true;
      this.inflight = this.$api.checkCampaignTracking(this.campaignId, this.payload).then((report) => {
        if (seq !== this.seq) return this.inflight;
        this.report = report;
        this.error = '';
        this.pending = false;
        if (report.links.untracked === 0) this.open = false;
        return report;
      }).catch((err) => {
        if (seq !== this.seq) return this.inflight;
        this.report = null;
        this.error = err.response?.data?.message || err.toString();
        this.pending = false;
        return null;
      });
      return this.inflight;
    },

    // The report for the content as it is now, for the start confirmation and the test
    // send: it does not wait for the typing pause.
    current() {
      if (this.timer || !this.inflight) return this.run();
      return this.inflight;
    },
  },
};
</script>

<style scoped>
.tc-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-bottom: 12px; }
.tc-head .title { margin: 0; }
.tc-rows { margin: 0 0 10px; padding: 0; list-style: none; }
.tc-row { display: flex; gap: 8px; align-items: flex-start; padding: 9px 0; border-top: 1px solid #ededed; }
.tc-row:first-child { border-top: 0; padding-top: 0; }
.tc-row > .icon { flex: none; margin-top: 1px; color: #b5b5b5; }
.tc-row.is-ok > .icon { color: #1f9d55; }
.tc-row.is-warn > .icon { color: #c27803; }
.tc-row.is-off > .icon { color: #d0342c; }
.tc-row p { margin: 0; overflow-wrap: anywhere; }
.tc-state { margin-left: 6px; }
.tc-row.is-warn .tc-state { color: #946200; font-weight: 600; }
.tc-row.is-off .tc-state { color: #d0342c; font-weight: 600; }
.tc-row .help { margin-top: 2px; line-height: 1.55; }
.tc-links {
  margin: 6px 0 0; padding: 6px 10px; background: #fafafa; border: 1px solid #ededed; border-radius: 4px;
  font-size: 12px; line-height: 1.7; overflow-wrap: anywhere;
}
.tc-foot { display: flex; justify-content: space-between; gap: 10px; align-items: flex-start; border-top: 1px solid #ededed; padding-top: 9px; }
.tc-foot .help { margin: 0; line-height: 1.55; }
.tc-foot a { white-space: nowrap; font-size: 12px; }
.tracking-check code { font-size: 11.5px; padding: 1px 4px; }
.tracking-check .notification { padding: 10px 12px; font-size: 13px; margin-bottom: 10px; }
</style>
