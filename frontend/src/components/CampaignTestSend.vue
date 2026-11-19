<template>
  <div class="box test-send" data-cy="test-send">
    <h3 class="title is-size-6">
      {{ $t('campaigns.sendTest') }}
    </h3>

    <b-field :type="rejected.length > 0 ? 'is-danger' : ''">
      <b-taginput :value="value" @input="onEmails" :before-adding="beforeAdding" :disabled="disabled"
        :maxtags="maxRecipients" :has-counter="false" icon="email-outline" :placeholder="$t('campaigns.testEmails')"
        data-cy="test-emails">
        <template #selected="{ tags }">
          <span v-for="(email, i) in tags" :key="email" class="tag" :class="{ 'is-rejected': kindOf(email) === 'rejected' }"
            :title="email" :data-kind="kindOf(email)">
            <span class="has-ellipsis">{{ email }}</span>
            <span class="kind" :class="kindOf(email)">{{ $t(`campaigns.testSend.kind.${kindOf(email)}`) }}</span>
            <a role="button" class="delete is-small" tabindex="0" :aria-label="$t('globals.buttons.delete')"
              @click.stop="removeEmail(i)" @keyup.enter.stop="removeEmail(i)" />
          </span>
        </template>
      </b-taginput>
    </b-field>
    <p v-if="rejected.length > 0" class="help is-danger rejected-help" role="alert" data-cy="test-rejected">
      {{ $t(ownUnverified ? 'campaigns.testSend.ownUnverifiedHelp' : 'campaigns.testSend.rejectedHelp') }}
    </p>
    <p v-if="canAddOwn" class="own-link">
      <a href="#" role="button" @click.prevent="addOwn" data-cy="test-add-own">
        <b-icon icon="plus" size="is-small" /> {{ $t('campaigns.testSend.addOwn') }}
      </a>
    </p>
    <p class="help" data-cy="test-help">{{ $t(helpKey, { domains: domains.join(', '), max: maxRecipients }) }}</p>

    <div class="sample">
      <div class="sample-head">
        <span>
          <strong>{{ $t('campaigns.testSend.sample') }}</strong>
          <span class="current" data-cy="test-sample-current"> · {{ currentLabel }}</span>
        </span>
        <a href="#" role="button" class="is-size-7" :aria-expanded="isSampleOpen ? 'true' : 'false'"
          @click.prevent="isSampleOpen = !isSampleOpen" data-cy="test-sample-toggle">
          {{ $t(isSampleOpen ? 'campaigns.testSend.collapse' : 'campaigns.testSend.change') }}
        </a>
      </div>

      <div v-if="isSampleOpen" class="sample-body">
        <p class="help mb-3">{{ $t('campaigns.testSend.sampleHelp') }}</p>

        <b-radio :value="sample.mode" @input="setMode" native-value="default" :disabled="disabled"
          data-cy="test-sample-default">
          {{ $t('campaigns.testSend.sampleDefault') }}
          <span class="has-text-grey is-size-7">{{ $t('campaigns.testSend.sampleDefaultHint') }}</span>
        </b-radio>

        <template v-if="canReadSubscribers">
          <b-radio :value="sample.mode" @input="setMode" native-value="subscriber" :disabled="disabled"
            data-cy="test-sample-subscriber">
            {{ $t('campaigns.testSend.sampleSubscriber') }}
          </b-radio>
          <div v-if="sample.mode === 'subscriber'" class="sub">
            <div v-if="sample.subscriber" class="picked" data-cy="test-sample-picked">
              <div>
                <strong>{{ sample.subscriber.name || sample.subscriber.email }}</strong>
                <small>{{ sample.subscriber.email }} ·
                  {{ $t('campaigns.testSend.attribCount', { num: attribCount(sample.subscriber) }) }}</small>
              </div>
              <a role="button" class="delete is-small" tabindex="0" :aria-label="$t('globals.buttons.delete')"
                @click="pick(null)" @keyup.enter="pick(null)" />
            </div>
            <b-autocomplete v-else v-model="search" :data="found" :loading="isSearching" field="email"
              :placeholder="$t('campaigns.testSend.subscriberSearch')" icon="magnify" size="is-small" clearable
              @typing="onSearch" @select="pick" data-cy="test-sample-search">
              <template #default="{ option }">
                <strong>{{ option.name }}</strong> <span class="has-text-grey">{{ option.email }}</span>
              </template>
              <template #empty>
                {{ $t(search.trim().length < 2 ? 'campaigns.testSend.subscriberSearchHint' : 'globals.messages.emptyState') }}
              </template>
            </b-autocomplete>
            <p class="help">{{ $t('campaigns.testSend.borrowHelp') }}</p>
          </div>
        </template>

        <b-radio :value="sample.mode" @input="setMode" native-value="custom" :disabled="disabled"
          data-cy="test-sample-custom">
          {{ $t('campaigns.testSend.sampleCustom') }}
        </b-radio>
        <div v-if="sample.mode === 'custom'" class="sub">
          <b-field :label="$t('globals.fields.name')" custom-class="is-small">
            <b-input :value="sample.name" @input="(v) => update({ name: v })" size="is-small" :maxlength="200"
              :has-counter="false" :disabled="disabled" data-cy="test-sample-name" />
          </b-field>
          <b-field :label="$t('campaigns.testSend.attribs')" custom-class="is-small">
            <b-input :value="sample.attribs" @input="(v) => update({ attribs: v })" type="textarea" size="is-small"
              class="attribs" :disabled="disabled" spellcheck="false" data-cy="test-sample-attribs" />
          </b-field>
        </div>

        <p v-if="sampleError" class="help is-danger" role="alert" data-cy="test-sample-error">{{ $t(sampleError) }}</p>
        <b-button size="is-small" type="is-light" icon-left="file-find-outline" :disabled="disabled || !!sampleError"
          @click="isPreviewing = true" data-cy="test-sample-preview">
          {{ $t('campaigns.testSend.previewWith') }}
        </b-button>
      </div>
    </div>

    <p class="help mt-3 mb-4">
      {{ $t(label.kind === 'default' ? 'campaigns.personalize.testHelp' : 'campaigns.testSend.allSample') }}
    </p>
    <b-field>
      <b-button @click="send" :loading="loading" :disabled="disabled" type="is-primary" icon-left="email-outline"
        data-cy="btn-test-send">
        {{ $t('campaigns.send') }}
      </b-button>
    </b-field>

    <campaign-preview v-if="isPreviewing" is-post @close="isPreviewing = false" type="campaign" :id="preview.id"
      :title="preview.title" :content-type="preview.contentType" :template-id="preview.templateId"
      :body="preview.body" :sample="preview.sample" :sample-label="preview.sampleLabel" />
  </div>
</template>

<script>
import { mapState } from 'vuex';

import CampaignPreview from './CampaignPreview.vue';
import {
  TEST_MAX_RECIPIENTS, addressKind, attribsTemplate, cleanEmail, sampleLabel, samplePayload,
} from '../test-send';

// The test box of the campaign page (LST-41): who receives the test, and the sample values
// for the addresses that are not subscribers.
export default {
  name: 'CampaignTestSend',

  components: { CampaignPreview },

  props: {
    // The addresses (v-model).
    value: { type: Array, default: () => [] },
    // The sample (.sync), as newSample() of test-send.js makes it.
    sample: { type: Object, required: true },
    // The addresses the server refused in the last request (.sync).
    rejected: { type: Array, default: () => [] },
    // The refused address is the user's own, and a mail to confirm it went to it.
    ownUnverified: { type: Boolean, default: false },
    disabled: { type: Boolean, default: false },
    loading: { type: Boolean, default: false },
    // What the preview renders: { id, title, contentType, templateId, body, sample, sampleLabel }.
    preview: { type: Object, default: () => ({}) },
  },

  data() {
    return {
      isSampleOpen: false,
      isPreviewing: false,
      search: '',
      found: [],
      isSearching: false,
      searchTimer: null,
      searchSeq: 0,
      maxRecipients: TEST_MAX_RECIPIENTS,
    };
  },

  computed: {
    ...mapState(['serverConfig', 'profile']),

    domains() { return this.serverConfig.test_domains || []; },

    // The address on the user's profile. The first test to it makes the server mail a link
    // that confirms it; until then the server refuses it like any other address.
    ownEmail() { return this.profile.type === 'api' ? '' : cleanEmail(this.profile.email); },

    canAddOwn() {
      return !this.disabled && !!this.ownEmail && this.value.length < this.maxRecipients
        && !this.value.map(cleanEmail).includes(this.ownEmail);
    },

    helpKey() { return this.domains.length > 0 ? 'campaigns.sendTestHelp' : 'campaigns.testSend.helpNoDomains'; },

    canReadSubscribers() { return this.$can('subscribers:get_all', 'subscribers:get'); },

    label() { return sampleLabel(this.sample); },

    sampleError() { return samplePayload(this.sample).error || ''; },

    currentLabel() {
      const name = this.label.name ? ` · ${this.label.name}` : '';
      return `${this.$t(`campaigns.testSend.current.${this.label.kind}`)}${name}`;
    },
  },

  methods: {
    kindOf(email) {
      if (this.rejected.includes(cleanEmail(email))) return 'rejected';
      return addressKind(email, this.ownEmail, this.domains);
    },

    beforeAdding(email) {
      const e = cleanEmail(email);
      return !!this.$utils.validateEmail(e) && !this.value.map(cleanEmail).includes(e);
    },

    onEmails(emails) {
      const list = emails.map(cleanEmail);
      this.$emit('input', list);
      // An address that was taken out is no longer marked.
      if (this.rejected.length > 0) this.$emit('update:rejected', this.rejected.filter((e) => list.includes(e)));
    },

    removeEmail(i) {
      if (this.disabled) return;
      this.onEmails(this.value.filter((_, n) => n !== i));
    },

    addOwn() {
      if (this.canAddOwn) this.onEmails([...this.value, this.ownEmail]);
    },

    update(change) {
      this.$emit('update:sample', { ...this.sample, ...change });
    },

    setMode(mode) {
      const change = { mode };
      // The first time, the typed-in attributes start as the standard keys.
      if (mode === 'custom' && !this.sample.attribs) {
        change.attribs = attribsTemplate(this.serverConfig.axm_standard_attribs);
      }
      this.update(change);
    },

    attribCount(sub) { return Object.keys(sub.attribs || {}).length; },

    pick(sub) {
      this.search = '';
      this.found = [];
      this.update({
        subscriber: sub ? {
          id: sub.id, name: sub.name, email: sub.email, attribs: sub.attribs || {},
        } : null,
      });
    },

    onSearch(text) {
      clearTimeout(this.searchTimer);
      const q = String(text || '').trim();
      if (q.length < 2) {
        this.found = [];
        this.isSearching = false;
        return;
      }
      this.isSearching = true;
      this.searchTimer = setTimeout(() => {
        this.searchSeq += 1;
        const seq = this.searchSeq;
        this.$api.getSubscriberAttribSample({ search: q.toLowerCase(), page: 1, per_page: 8 }).then((data) => {
          // Only the answer to the latest search is shown.
          if (seq === this.searchSeq) this.found = data.results || [];
        }).catch(() => {
          if (seq === this.searchSeq) this.found = [];
        }).finally(() => {
          if (seq === this.searchSeq) this.isSearching = false;
        });
      }, 250);
    },

    send() {
      if (this.sampleError) {
        this.isSampleOpen = true;
        return;
      }
      this.$emit('send');
    },
  },

  beforeDestroy() {
    clearTimeout(this.searchTimer);
  },
};
</script>

<style scoped>
.test-send >>> .taginput .taginput-container .tag {
  max-width: calc(100% - 0.6rem); display: inline-flex; align-items: center;
}
.test-send >>> .taginput .tag .has-ellipsis { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.test-send >>> .taginput .tag .kind,
.test-send >>> .taginput .tag .delete { flex-shrink: 0; }
.test-send >>> .taginput .tag .kind {
  font-size: 0.7rem; font-weight: 600; margin-left: 0.45em; padding: 0 0.45em; border-radius: 3px; line-height: 1.5;
  white-space: nowrap;
}
.test-send >>> .taginput .tag .kind.own,
.test-send >>> .taginput .tag .kind.internal { background: #e3f1e5; color: #1c6b2c; }
.test-send >>> .taginput .tag .kind.check { background: #eef0f3; color: #555; }
.test-send >>> .taginput .tag .kind.rejected { background: #fbe1e1; color: #b3261e; }
.test-send >>> .taginput .tag.is-rejected { background: #fdecec; color: #b3261e; box-shadow: inset 0 0 0 1px #f1a9a4; }
.test-send >>> .taginput .tag .delete { margin-left: 0.4rem; }
.rejected-help { font-size: 0.8rem; margin-top: -0.4rem; }
.own-link { font-size: 0.8rem; margin: 0.35rem 0 0.2rem; }
.sample { border-top: 1px solid #e6e6e6; margin-top: 1rem; padding-top: 0.8rem; }
.sample-head { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; font-size: 0.85rem; }
.sample-head strong { font-weight: 600; }
.sample-head .current { color: #666; overflow-wrap: anywhere; }
.sample-body { margin-top: 0.7rem; }
.sample-body .b-radio { display: flex; margin: 0 0 0.45rem 0; }
.sample-body .sub { margin: 0 0 0.7rem 1.75rem; }
.picked {
  display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; border: 1px solid #dbdbdb;
  border-radius: 4px; padding: 0.4rem 0.6rem; font-size: 0.85rem; background: #fafafa; overflow-wrap: anywhere;
}
.picked small { color: #666; display: block; }
.picked .delete { flex-shrink: 0; }
.sample-body >>> .attribs textarea { font-family: monospace; font-size: 0.8rem; min-height: 6.5em; }
.sample-body >>> .label.is-small { font-size: 0.75rem; margin-bottom: 0.2rem; }
</style>
