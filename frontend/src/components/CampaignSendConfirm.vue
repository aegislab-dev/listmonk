<template>
  <div class="modal-card send-confirm" style="width: auto" data-cy="send-confirm" :data-state="state" :data-mode="model ? model.mode : ''">
    <header class="modal-card-head">
      <div>
        <h4 class="send-title" data-cy="send-title" role="status" :data-stale="stale ? 'yes' : null">{{ headTitle }}</h4>
        <p class="help mt-1" data-cy="send-sub">{{ headSub }}</p>
      </div>
    </header>

    <section class="modal-card-body">
      <!-- Loading -->
      <div v-if="state === 'loading'" data-cy="send-loading">
        <div v-if="campaignName" class="send-row" data-cy="send-campaign">
          <div class="send-k">{{ $t('campaigns.sendConfirm.row.campaign') }}</div>
          <div class="send-v">{{ campaignName }}</div>
        </div>
        <div class="send-row">
          <div class="send-k">{{ $t('campaigns.sendConfirm.row.lists') }}</div>
          <div class="send-v"><b-skeleton :count="3" /></div>
        </div>
        <p class="help">{{ $t('campaigns.sendConfirm.loadingNote') }}</p>
      </div>

      <!-- The checks could not be loaded: try again, or go on without the count. -->
      <div v-else-if="state === 'error'" data-cy="send-error">
        <div class="send-row" data-cy="send-campaign">
          <div class="send-k">{{ $t('campaigns.sendConfirm.row.campaign') }}</div>
          <div class="send-v">{{ campaignName }}</div>
        </div>
        <div v-if="campaignSubject" class="send-row" data-cy="send-subject">
          <div class="send-k">{{ $t('campaigns.sendConfirm.row.subject') }}</div>
          <div class="send-v">{{ campaignSubject }}</div>
        </div>
        <div class="send-checks" data-cy="send-checks">
          <div class="send-checks-head">{{ $t('campaigns.sendConfirm.checks') }}</div>
          <div class="send-line">
            <span class="send-icon lv-warn">!</span>
            <span class="send-line-text">
              {{ $t('campaigns.sendConfirm.checkFailed') }}
              <span v-if="error" class="has-text-grey" data-cy="send-error-reason">{{ error }}</span>
            </span>
            <b-button size="is-small" class="send-retry" data-cy="send-retry" @click="load">
              {{ $t('campaigns.sendConfirm.retry') }}
            </b-button>
          </div>
        </div>
        <p class="help">{{ $t('campaigns.sendConfirm.blindHelp') }}</p>
      </div>

      <template v-else-if="model">
        <div class="send-rows">
          <div class="send-row" data-cy="send-campaign">
            <div class="send-k">{{ $t('campaigns.sendConfirm.row.campaign') }}</div>
            <div class="send-v">{{ pf.name }}</div>
          </div>
          <!-- Read again after a change (approved design A7): the campaign and its recipients,
               and of the other rows only those that changed, with what they were. -->
          <div v-if="shows('subject')" class="send-row" data-cy="send-subject">
            <div class="send-k">{{ $t('campaigns.sendConfirm.row.subject') }}</div>
            <div class="send-v">
              <span data-cy="send-subject-text">{{ pf.subject }}</span>
              <div v-if="subjectNoteKey" class="help" data-cy="send-subject-rendered" :data-note="pf.subjectNote">{{ $t(subjectNoteKey) }}</div>
              <div v-if="stale" class="send-was" data-cy="send-subject-was">{{ $t('campaigns.sendConfirm.wasText', { text: prev.subject }) }}</div>
            </div>
          </div>
          <div v-if="shows('from')" class="send-row" data-cy="send-from">
            <div class="send-k">{{ $t('campaigns.sendConfirm.row.from') }}</div>
            <div class="send-v">
              {{ pf.fromEmail }}
              <span class="has-text-grey">
                · {{ pf.replyTo ? $t('campaigns.sendConfirm.replyTo', { email: pf.replyTo }) : $t('campaigns.sendConfirm.replyOwn') }}
              </span>
              <div v-if="pf.senderPerSubscriber" class="help" data-cy="send-from-per-subscriber">{{ $t('campaigns.sendConfirm.senderPerSubscriber') }}</div>
              <div v-if="stale" class="send-was" data-cy="send-from-was">{{ $t('campaigns.sendConfirm.wasText', { text: prev.from }) }}</div>
            </div>
          </div>
          <div class="send-row" data-cy="send-lists">
            <div class="send-k">{{ $t('campaigns.sendConfirm.row.lists') }}</div>
            <div class="send-v">
              <div v-for="(l, i) in pf.lists" :key="`${l.id}-${i}`" class="send-list" :data-cy="`send-list-${l.id || 'deleted'}`">
                <b-tag type="is-info is-light">{{ l.name }}</b-tag>
                <b-tag v-if="l.dynamic" type="is-light">{{ $t('campaigns.sendConfirm.dynamic') }}</b-tag>
                <b-tag v-if="l.deleted" type="is-danger is-light">{{ $t('campaigns.sendConfirm.deleted') }}</b-tag>
                <strong v-if="!isResume" class="ml-1" data-cy="send-list-count">{{ $t('campaigns.sendConfirm.people', { n: l.count }) }}</strong>
                <span v-if="wasCount(l) !== null" class="send-was" data-cy="send-list-was">
                  {{ $t('campaigns.sendConfirm.was', { n: wasCount(l) }) }}
                </span>
              </div>
              <div v-if="!isResume && (sumParts.length || totalWas !== null)" class="send-sum" data-cy="send-recipients">
                {{ sumParts.length ? sumParts.join(' · ') : $t('campaigns.sendConfirm.total', { n: pf.recipients }) }}
                <span v-if="totalWas !== null" class="send-was" data-cy="send-recipients-was">{{ $t('campaigns.sendConfirm.was', { n: totalWas }) }}</span>
              </div>
              <div v-if="model.zero" class="send-sum lv-block-text" data-cy="send-zero">{{ $t('campaigns.sendConfirm.zeroSummary') }}</div>
            </div>
          </div>

          <!-- When -->
          <div v-if="model.mode === 'start' && shows('when')" class="send-row" data-cy="send-when">
            <div class="send-k">{{ $t('campaigns.sendConfirm.row.when') }}</div>
            <div class="send-v">
              <strong>{{ $t('campaigns.sendConfirm.now') }}</strong>
              <div v-if="stale" class="send-was" data-cy="send-when-was">{{ $t('campaigns.sendConfirm.wasText', { text: prev.when }) }}</div>
            </div>
          </div>
          <div v-if="model.when && shows('when')" class="send-row" data-cy="send-schedule">
            <div class="send-k">{{ $t('campaigns.sendConfirm.row.schedule') }}</div>
            <div class="send-v">
              <strong data-cy="send-schedule-time">{{ model.when.text }}</strong>
              <div data-cy="send-schedule-zone">
                {{ $t('campaigns.sendConfirm.zone', { zone: model.when.zone, offset: model.when.offset }) }}
              </div>
              <div v-if="until" class="has-text-grey" data-cy="send-schedule-until">{{ $t(until.key, until.params) }}</div>
              <div v-if="model.other" class="send-tz-other" data-cy="send-schedule-other">
                {{ $t('campaigns.sendConfirm.zoneOther', { kst: `${model.other.text} (${model.other.offset})` }) }}
              </div>
              <div v-if="stale" class="send-was" data-cy="send-when-was">{{ $t('campaigns.sendConfirm.wasText', { text: prev.when }) }}</div>
            </div>
          </div>
          <div v-if="isResume" class="send-row" data-cy="send-progress">
            <div class="send-k">{{ $t('campaigns.sendConfirm.row.progress') }}</div>
            <div class="send-v">
              <progress class="progress is-small is-info mb-1" :value="pf.sent" :max="Math.max(pf.toSend, pf.sent, 1)" />
              <div class="has-text-grey" data-cy="send-progress-text">{{ progressText }}</div>
              <div class="send-sum" data-cy="send-recipients">
                {{ $t('campaigns.sendConfirm.remaining', { n: pf.remaining }) }}
                <span v-if="totalWas !== null" class="send-was" data-cy="send-recipients-was">{{ $t('campaigns.sendConfirm.was', { n: totalWas }) }}</span>
              </div>
            </div>
          </div>
        </div>

        <div class="send-checks" data-cy="send-checks">
          <div class="send-checks-head">{{ $t('campaigns.sendConfirm.checks') }}</div>
          <ul>
            <li v-for="c in model.checks" :key="`${c.name}-${c.key}`" class="send-line" :class="`lv-${c.level}`"
              :data-cy="`send-check-${c.name}`" :data-level="c.level">
              <span class="send-icon" :class="`lv-${c.level}`">{{ icons[c.level] }}</span>
              <span>
                <template v-for="(part, i) in lineParts(c)">
                  <strong v-if="part.bold" :key="i">{{ part.text }}</strong>
                  <code v-else-if="part.code" :key="i">{{ part.text }}</code>
                  <template v-else>{{ part.text }}</template>
                </template>
              </span>
            </li>
          </ul>
        </div>

        <p v-if="model.asOf" class="help" data-cy="send-asof">
          {{ $t(isResume ? 'campaigns.sendConfirm.asOfResume' : 'campaigns.sendConfirm.asOf', { time: asOfText }) }}
        </p>
      </template>
    </section>

    <footer class="modal-card-foot has-text-right">
      <b-button data-cy="send-cancel" :disabled="busy" @click="$parent.close()">{{ $t('globals.buttons.cancel') }}</b-button>
      <b-button v-if="state === 'error'" type="is-warning" data-cy="send-ok" :loading="busy" @click="confirm(true)">
        {{ $t('campaigns.sendConfirm.confirmBlind') }}
      </b-button>
      <b-button v-else type="is-primary" data-cy="send-ok" :class="{ 'send-ok-warn': model && model.warnings }"
        :disabled="state !== 'ready' || !model || model.disabled" :loading="busy" @click="confirm(false)">
        {{ okText }}
      </b-button>
    </footer>
  </div>
</template>

<script>
import Vue from 'vue';
import dayjs from 'dayjs';
import { richParts, sendConfirmModel, untilKey } from '../send-confirm';
import { splitAttribs, TZ_ATTRIB } from '../schedule-time';

// What a reading of the checks shows, to tell whether a new reading differs from the one on
// screen. Not the times of the reading itself, nor the passed send time that the window adds
// on its own a few seconds early.
const fingerprint = (pf) => JSON.stringify([
  pf.mode, pf.sendAt, pf.sendTz, pf.subject, pf.subjectNote, pf.fromEmail, pf.replyTo, pf.senderPerSubscriber,
  pf.copies, pf.recipients, pf.remaining, (pf.lists || []).map((l) => [l.id, l.count, l.deleted]),
  (pf.checks || []).filter((c) => c.key !== 'schedule.past').map((c) => [c.key, c.level, c.params]),
  pf.tracking ? [pf.tracking.state, pf.tracking.view, pf.tracking.links && pf.tracking.links.untracked] : null,
  pf.trackingError || '',
]);

// Seconds before the send time that the window already says it has passed.
const PAST_MARGIN = 3000;

// The window that every start, resume and schedule of a campaign goes through (LST-48):
// the people it reaches, its lists, subject, sender, time and the checks, read from the
// server at the moment it opens. Confirming changes the status only; the campaign is saved
// before the window opens and the server refuses a campaign that changed after this read.
export default Vue.extend({
  name: 'CampaignSendConfirm',

  props: {
    campaignId: { type: Number, required: true },
    // The status that confirming asks for: 'running' (start, resume) or 'scheduled'.
    status: { type: String, required: true },
    // What the window says while it has not read the campaign, or cannot: which campaign it is.
    campaignName: { type: String, default: '' },
    campaignSubject: { type: String, default: '' },
  },

  data() {
    return {
      state: 'loading', // loading | ready | error
      pf: null,
      error: '',
      busy: false,
      stale: false,
      staleWhy: '',
      // What the window showed before the campaign changed under it: the count and each list's.
      prev: null,
      ctl: null,
      ticker: null,
      // The server's clock minus this browser's, from the last reading: the window keeps
      // time by the server, as the send does (a browser clock may be behind).
      skew: 0,
      now: new Date(),
      icons: {
        ok: '✓', warn: '!', block: '✕', info: 'i',
      },
    };
  },

  computed: {
    isResume() { return this.pf && this.pf.mode === 'resume'; },

    // The time zone the scheduled time is shown in: the one it was chosen in on the campaign
    // page (LST-50, attribs.axm_send_tz), Korean time when none is stored, as the picker does.
    zone() { return splitAttribs({ [TZ_ATTRIB]: this.pf ? this.pf.sendTz : '' }).tz; },

    lang() { return (this.$store.state.serverConfig || {}).lang || 'ko'; },

    model() {
      return this.pf ? sendConfirmModel(this.pf, { tz: this.zone, lang: this.lang, now: this.now }) : null;
    },

    until() { return this.model && this.model.until ? untilKey(this.model.until) : null; },

    // The summary under the lists: people on several lists, and people already left out.
    sumParts() {
      const m = this.model;
      const out = [];
      if (!m || m.zero) return out;
      if (this.pf.lists.length > 1) {
        out.push(m.overlap > 0
          ? this.$t('campaigns.sendConfirm.overlap', { overlap: m.overlap, n: this.pf.recipients })
          : this.$t('campaigns.sendConfirm.total', { n: this.pf.recipients }));
      }
      if (m.excluded > 0) out.push(this.$t('campaigns.sendConfirm.excluded', { excluded: m.excluded }));
      return out;
    },

    progressText() {
      const p = { sent: this.pf.sent, total: Math.max(this.pf.toSend, this.pf.sent) };
      if (!this.pf.pausedAt) return this.$t('campaigns.sendConfirm.progress', p);
      return this.$t('campaigns.sendConfirm.progressPaused', { ...p, time: dayjs(this.pf.pausedAt).format('YYYY-MM-DD HH:mm') });
    },

    // The count of recipients before and after a change under the open window.
    countChange() {
      if (!this.prev || !this.model || this.prev.count === this.model.count) return null;
      return { before: this.prev.count, after: this.model.count };
    },

    // What the rows other than the campaign and its recipients say now, to tell after a
    // change under the window which of them changed.
    rowTexts() {
      if (!this.pf || !this.model) return null;
      const { pf } = this;
      const reply = pf.replyTo ? this.$t('campaigns.sendConfirm.replyTo', { email: pf.replyTo }) : this.$t('campaigns.sendConfirm.replyOwn');
      return {
        subject: pf.subject,
        from: `${pf.fromEmail} · ${reply}`,
        // With its zone: the same wall time in another zone is another moment (LST-48 review).
        when: this.model.when
          ? `${this.model.when.text} · ${this.model.when.zone} · ${this.model.when.offset}`
          : this.$t('campaigns.sendConfirm.now'),
      };
    },

    // The note under the subject: rendered for the example subscriber, could not be, or varies.
    subjectNoteKey() {
      const note = this.pf && (this.pf.subjectNote || (this.pf.subjectRendered ? 'rendered' : ''));
      return {
        rendered: 'campaigns.sendConfirm.subjectRendered',
        unrendered: 'campaigns.sendConfirm.subjectUnrendered',
        varies: 'campaigns.sendConfirm.subjectVaries',
      }[note] || '';
    },

    // The count before the change, shown next to the total. With one list that kept its
    // place, the list's own "before" already says it.
    totalWas() {
      const ch = this.countChange;
      if (!ch) return null;
      const lists = this.pf.lists || [];
      if (!this.isResume && lists.length === 1 && this.wasCount(lists[0]) !== null) return null;
      return ch.before;
    },

    asOfText() { return this.model && this.model.asOf ? dayjs(this.model.asOf).format('YYYY-MM-DD HH:mm') : ''; },

    headTitle() {
      if (this.state === 'loading') return this.$t('campaigns.sendConfirm.title.loading');
      if (this.state === 'error') return this.$t('campaigns.sendConfirm.title.error');
      // Read again after the campaign changed under the window (approved design A7).
      if (this.stale) return this.$t('campaigns.sendConfirm.title.stale');
      return this.$t(this.model.title.key, this.model.title.params);
    },

    headSub() {
      if (this.state === 'loading') return this.$t('campaigns.sendConfirm.sub.loading');
      if (this.state === 'error') return this.$t('campaigns.sendConfirm.sub.error');
      if (this.stale) return this.$t(this.staleWhy === 'checks' ? 'campaigns.sendConfirm.sub.staleChecks' : 'campaigns.sendConfirm.sub.stale');
      return this.$t(this.model.sub.key, this.model.sub.params);
    },

    okText() {
      if (!this.model) return this.$t('globals.buttons.ok');
      return this.$t(this.model.confirm.key, this.model.confirm.params);
    },
  },

  methods: {
    // Whether a row other than the campaign and its recipients is shown: always, except in the
    // window read again after a change, where only a row that changed is (approved design A7).
    shows(row) {
      if (!this.stale || !this.prev || !this.rowTexts) return true;
      return this.prev[row] !== this.rowTexts[row];
    },

    // The count a list had before the campaign changed under the window, if it is another now.
    wasCount(l) {
      if (!this.prev || this.isResume) return null;
      const was = this.prev.lists[`${l.id}`];
      return was === undefined || was === l.count ? null : was;
    },

    // The status to ask for. What was read decides: a campaign that was changed under the window
    // (a schedule taken off, or put on) is started or scheduled as it is now, not as it was
    // when the window was opened. Without a reading, the one the caller asked for.
    statusToAsk(blind) {
      if (blind || !this.pf) return this.status;
      return this.pf.mode === 'schedule' ? 'scheduled' : 'running';
    },

    // The send time passed while the window is open: the status change now sends at once.
    // Say so the way the server does (schedule.past) and tell whether it was added now.
    markPast() {
      const { pf } = this;
      if (!pf || this.state !== 'ready' || !pf.sendAt) return false;
      if (pf.mode !== 'schedule' && pf.mode !== 'resume') return false;
      // A few seconds early: the request takes time to arrive.
      if (new Date(pf.sendAt).getTime() > this.now.getTime() + PAST_MARGIN) return false;
      if (pf.checks.some((c) => c.key === 'schedule.past')) return false;
      pf.checks.push({ key: 'schedule.past', level: 'warn', params: {} });
      return true;
    },

    serverNow() { return new Date(Date.now() + this.skew); },

    // Whether the window says that the send time has passed.
    pastShown() { return !!this.pf && this.pf.checks.some((c) => c.key === 'schedule.past'); },

    tick() {
      this.now = this.serverNow();
      this.markPast();
    },

    lineParts(c) {
      return richParts(this.$t(c.key, c.params));
    },

    async load() {
      if (this.ctl) this.ctl.abort();
      this.ctl = typeof AbortController !== 'undefined' ? new AbortController() : null;
      this.state = 'loading';
      this.error = '';
      try {
        const pf = await this.$api.getCampaignPreflight(this.campaignId, this.ctl ? { signal: this.ctl.signal } : {});
        this.skew = pf.now ? new Date(pf.now).getTime() - Date.now() : 0;
        if (!Number.isFinite(this.skew)) this.skew = 0;
        this.pf = pf;
        this.now = this.serverNow();
        this.state = 'ready';
      } catch (err) {
        if (err && (err.code === 'ERR_CANCELED' || err.name === 'CanceledError')) return;
        this.pf = null;
        this.state = 'error';
        this.error = (err.response && err.response.data && err.response.data.message) || '';
      }
    },

    // Keeps what the window shows, to say what changed when it is read again (why: 'campaign'
    // edited, or 'checks' that came out otherwise).
    rememberShown(why) {
      this.prev = {
        ...this.rowTexts,
        count: this.model.count,
        lists: Object.fromEntries(this.pf.lists.map((l) => [`${l.id}`, l.count])),
      };
      this.stale = true;
      this.staleWhy = why;
    },

    // Shows a reading that differs from the one on screen, as after a change.
    showChanged(fresh, why) {
      this.rememberShown(why);
      this.skew = fresh.now ? new Date(fresh.now).getTime() - Date.now() : this.skew;
      if (!Number.isFinite(this.skew)) this.skew = 0;
      this.pf = fresh;
      this.now = this.serverNow();
      this.markPast();
    },

    // blind: the checks could not be loaded; go on without them and without the
    // changed-since check (there is nothing that was shown).
    async confirm(blind) {
      if (this.busy) return;
      // The send time may have passed since the last tick: show that first, do not send.
      this.now = this.serverNow();
      if (!blind && this.markPast()) return;
      this.busy = true;
      try {
        // Read the checks again: what they say can change without the campaign changing (a
        // shared template edited elsewhere, a list that grew), which the server's updated_at
        // check does not see. Anything shown that differs is shown again first (LST-48 review).
        if (!blind) {
          const fresh = await this.$api.getCampaignPreflight(this.campaignId).catch(() => null);
          if (fresh && fingerprint(fresh) !== fingerprint(this.pf)) {
            this.showChanged(fresh, fresh.updatedAt !== this.pf.updatedAt ? 'campaign' : 'checks');
            return;
          }
        }
        const out = await this.$api.changeCampaignStatus(
          this.campaignId,
          this.statusToAsk(blind),
          blind ? undefined : this.pf.updatedAt,
          { disableToast: true },
          // The server refuses a passed send time that the window did not show (412).
          !blind && this.pastShown() ? { send_at_passed: true } : {},
        );
        this.$emit('done', out);
        this.$parent.close();
      } catch (err) {
        if (!blind && err.response && err.response.status === 412) {
          // The send time has come by the server's clock, and the window did not say so: read
          // again, which shows it.
          this.busy = false;
          await this.load();
          this.markPast();
          return;
        }
        if (!blind && err.response && err.response.status === 409) {
          // Edited after it was read: show what is true now and ask again.
          this.rememberShown('campaign');
          this.busy = false;
          await this.load();
          return;
        }
        this.$utils.toast((err.response && err.response.data && err.response.data.message) || err.message, 'is-danger');
        this.$emit('failed', err);
        this.$parent.close();
      } finally {
        this.busy = false;
      }
    },
  },

  watch: {
    // While the status change is on its way the window must stay: closing it would lose
    // the answer, and the page would not learn that the campaign was started (LST-48 review).
    busy(b) { this.$emit('busy', b); },
  },

  mounted() {
    this.load();
    // The time left and the passing of the send time are kept up to date while it is open.
    this.ticker = setInterval(this.tick, 1000);
  },

  beforeDestroy() {
    clearInterval(this.ticker);
    if (this.ctl) this.ctl.abort();
  },
});
</script>

<style scoped>
.send-confirm { max-width: 600px; }
/* The foot is transparent in this app: without a ground the buttons float over the page. */
.send-confirm .modal-card-foot { background: #fff; border-top: 1px solid #ededed; border-radius: 0 0 6px 6px; justify-content: flex-end; gap: 0.5rem; }
.send-title { font-size: 1.25rem; font-weight: 700; }
.send-row { display: grid; grid-template-columns: 6.5rem 1fr; gap: 0.75rem; padding: 0.3rem 0; }
.send-k { color: #7a7a7a; }
.send-v { min-width: 0; overflow-wrap: anywhere; }
.send-list { margin: 0.1rem 0; }
/* A long list name wraps instead of widening the window (390px). */
.send-list .tag { white-space: normal; height: auto; max-width: 100%; overflow-wrap: anywhere; text-align: left; }
.send-sum { margin-top: 0.3rem; padding: 0.3rem 0.5rem; background: #f5f5f5; border-radius: 6px; }
.send-was { color: #b25e00; font-size: 0.8rem; margin-left: 0.25rem; }
.lv-block-text { color: #cc0f35; }
.send-tz-other { margin-top: 0.4rem; padding: 0.4rem 0.6rem; background: #fffaeb; border: 1px solid #ffe08a; border-radius: 6px; font-size: 0.875rem; }
.send-checks { border: 1px solid #dbdbdb; border-radius: 8px; padding: 0.5rem 0.75rem; margin: 0.75rem 0 0.5rem; }
.send-checks-head { font-weight: 700; margin-bottom: 0.25rem; }
.send-line { display: flex; gap: 0.6rem; padding: 0.15rem 0; align-items: baseline; }
.send-line-text { min-width: 0; }
.send-retry { flex: none; }
.send-line.lv-block { background: #feecf0; border-radius: 6px; padding: 0.4rem 0.5rem; }
.send-ok-warn.button.is-primary { background-color: #b25e00; }
.send-icon { flex: none; width: 1rem; font-weight: 700; text-align: center; }
.send-icon.lv-ok { color: #1a7f37; }
.send-icon.lv-warn { color: #b25e00; }
.send-icon.lv-block { color: #cc0f35; }
@media (max-width: 480px) {
  .send-row { grid-template-columns: 1fr; gap: 0; }
  .modal-card-foot .button { flex: 1; }
}
</style>
