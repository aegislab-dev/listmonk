<template>
  <section class="campaign">
    <header class="columns page-header">
      <div class="column is-6">
        <p v-if="isEditing && data.status" class="tags">
          <b-tag v-if="isEditing" :class="data.status">
            {{ $t(`campaigns.status.${data.status}`) }}
          </b-tag>
          <b-tag v-if="data.type === 'optin'" :class="data.type">
            {{ $t('lists.optin') }}
          </b-tag>
          <span v-if="isEditing" class="has-text-grey-light is-size-7" :data-campaign-id="data.id">
            {{ $t('globals.fields.id') }}: <copy-text :text="`${data.id}`" />
            {{ $t('globals.fields.uuid') }}: <copy-text :text="data.uuid" />
          </span>
        </p>
        <h4 v-if="isEditing" class="title is-4">
          {{ data.name }}
        </h4>
        <h4 v-else class="title is-4">
          {{ $t('campaigns.newCampaign') }}
        </h4>
      </div>

      <div class="column is-6">
        <div v-if="canManage || canSend" class="buttons">
          <b-field grouped v-if="isEditing && canEdit">
            <b-field v-if="canManage" expanded>
              <b-button expanded @click="() => onSubmit('update')" :loading="loading.campaigns || savingCampaign" type="is-primary"
                icon-left="content-save-outline" data-cy="btn-save" aria-keyshortcuts="ctrl+s">
                <span class="has-kbd">{{ $t('globals.buttons.saveChanges') }} <span class="kbd">Ctrl+S</span></span>
              </b-button>
            </b-field>
            <b-field expanded v-if="canSend && canStart">
              <b-button expanded @click="startCampaign" :loading="loading.campaigns || savingCampaign" type="is-primary"
                icon-left="rocket-launch-outline" data-cy="btn-start">
                {{ $t('campaigns.start') }}
              </b-button>
            </b-field>
            <b-field expanded v-if="canSend && canSchedule">
              <b-button expanded @click="startCampaign" :loading="loading.campaigns || savingCampaign" type="is-primary"
                icon-left="clock-start" data-cy="btn-schedule">
                {{ $t('campaigns.schedule') }}
              </b-button>
            </b-field>
            <b-field expanded v-if="canSend && canUnSchedule">
              <b-button expanded @click="$utils.confirm(null, unscheduleCampaign)" :loading="loading.campaigns"
                type="is-primary" icon-left="clock-start" data-cy="btn-unschedule">
                {{ $t('campaigns.unSchedule') }}
              </b-button>
            </b-field>
          </b-field>
        </div>
      </div>
    </header>

    <b-loading :active="loading.campaigns || savingCampaign" />

    <campaign-followup-banner v-if="isEditing && data.id" :campaign-id="data.id" :status="data.status"
      :updated-at="data.updatedAt" @loaded="onFollowupLoaded" />
    <b-modal has-modal-card scroll="keep" :aria-modal="true" :active.sync="isSendVisible" :width="600" trap-focus
      :can-cancel="sendAsk && sendAsk.busy ? false : ['escape', 'x', 'outside']"
      @close="onSendClosed">
      <campaign-send-confirm v-if="sendAsk" :campaign-id="sendAsk.id" :status="sendAsk.status"
        :campaign-name="sendAsk.name"
        @busy="(b) => { if (sendAsk) sendAsk.busy = b; }" @done="onSendDone" @failed="onSendFailed" />
    </b-modal>

    <b-tabs type="is-boxed" :animated="false" v-model="activeTab" @input="onTab">
      <b-tab-item :label="$tc('globals.terms.campaign')" label-position="on-border" value="campaign"
        icon="rocket-launch-outline">
        <section class="wrap">
          <b-notification v-if="replyError" type="is-danger is-light" :closable="false" role="alert">
            {{ replyError }}
          </b-notification>
          <div class="columns">
            <div class="column is-7">
              <form @submit.prevent="() => onSubmit(isNew ? 'create' : 'update')">
                <b-field :label="$t('globals.fields.name')" label-position="on-border">
                  <b-input :maxlength="200" :ref="'focus'" v-model="form.name" name="name" :disabled="!canEdit"
                    :placeholder="$t('globals.fields.name')" required autofocus />
                </b-field>

                <b-field :label="$t('campaigns.subject')" label-position="on-border">
                  <b-input :maxlength="5000" v-model="form.subject" name="subject" :disabled="!canEdit"
                    :placeholder="$t('campaigns.subject')" required />
                </b-field>

                <template v-if="senderPolicyEnabled">
                  <div class="field">
                    <p class="label is-size-7">{{ $t('campaigns.sentFrom') }}</p>
                    <div class="sender-readonly" data-cy="sent-from">
                      <span>{{ resolvedFrom }}</span><span class="has-text-grey is-size-7">{{ $t('senders.automatic') }}</span>
                    </div>
                    <p class="help">{{ $t(usesAccountFrom ? 'campaigns.sentFromAccountHelp' : 'campaigns.sentFromDefaultHelp') }}</p>
                  </div>
                  <sender-address-input ref="replyTo" v-model="form.replyTo" :label="$t('campaigns.replyTo')"
                    :disabled="!canEdit" @addresses="onReplyAddresses" />
                </template>
                <b-notification v-if="!senderPolicyEnabled && fromWarning" type="is-warning" :closable="false"
                  class="mb-4" data-cy="from-warning" role="alert">
                  {{ fromWarning }}
                </b-notification>
                <b-field v-if="useFromSelect" :label="$t('campaigns.fromAddress')" label-position="on-border"
                  :message="$t('campaigns.fromAddressListHelp')">
                  <b-select v-model="fromSelected" name="from_email" :placeholder="$t('campaigns.fromAddressPick')"
                    :disabled="!canEdit" required expanded data-cy="from-select">
                    <option v-for="a in fromList" :value="a" :key="a">{{ a }}</option>
                  </b-select>
                </b-field>
                <b-field v-else-if="!senderPolicyEnabled" :label="$t('campaigns.fromAddress')" label-position="on-border">
                  <b-input :maxlength="200" v-model="form.fromEmail" name="from_email" :disabled="!canEdit"
                    :placeholder="$t('campaigns.fromAddressPlaceholder')" required />
                </b-field>

                <list-selector v-model="form.lists" :selected="form.lists" :all="lists.results" :disabled="!canEdit"
                  :label="$t('globals.terms.lists')" :placeholder="$t('campaigns.sendToLists')" />

                <address-check v-if="canShowAddressCheck" :list-ids="recentListIds" :lists="lists.results || []"
                  :optin="data.type === 'optin'" />

                <recent-send-exclude v-if="canShowRecentRule && !isFollowup" v-model="recentRule" :campaign-id="data.id || createdDraftId"
                  :list-ids="recentListIds" :campaign-type="data.type" :disabled="!canManage || !canEdit || recentStarted || !recentRuleLoaded || savingCampaign"
                  :started="recentStarted" :lists-editable="canChangeLists" :saved-campaign="!!data.id" :made-list="madeListView"
                  @list-made="onRecentListMade" />
                <p v-if="recentRuleError" class="help is-danger mb-4" role="alert">{{ recentRuleError }}</p>
                <p v-if="isFollowup && canShowRecentRule" class="help mb-4" data-cy="followup-exclude-note">
                  {{ $t('campaigns.followup.excludeNote') }}
                </p>

                <div class="columns">
                  <div class="column is-6">
                    <b-field :label="$tc('globals.terms.messenger')" label-position="on-border">
                      <b-select :placeholder="$tc('globals.terms.messenger')" v-model="form.messenger" name="messenger"
                        :disabled="!canEdit" required expanded>
                        <template v-if="emailMessengers.length > 1">
                          <optgroup label="email">
                            <option v-for="m in emailMessengers" :value="m" :key="m">
                              {{ m }}
                            </option>
                          </optgroup>
                        </template>
                        <template v-else>
                          <option value="email">email</option>
                        </template>
                        <option v-for="m in otherMessengers" :value="m" :key="m">{{ m }}</option>
                      </b-select>
                    </b-field>
                  </div>
                  <div class="column is-6">
                    <b-field :label="$t('campaigns.format')" label-position="on-border" class="mr-4 mb-0">
                      <b-select v-model="form.content.contentType" :disabled="!canEdit || isEditing" value="richtext"
                        expanded>
                        <option v-for="(name, f) in contentTypes" :key="f" name="format" :value="f"
                          :data-cy="`check-${f}`">
                          {{ name }}
                        </option>
                      </b-select>
                    </b-field>
                  </div>
                </div>

                <b-field :label="$t('globals.terms.tags')" label-position="on-border">
                  <b-taginput v-model="form.tags" name="tags" :disabled="!canEdit" ellipsis icon="tag-outline"
                    :placeholder="$t('globals.terms.tags')" />
                </b-field>
                <hr />

                <div class="columns">
                  <div class="column is-4">
                    <b-field :label="$t('campaigns.sendLater')" data-cy="btn-send-later">
                      <b-switch v-model="form.sendLater" :disabled="!canEdit" />
                    </b-field>
                  </div>
                  <div class="column">
                    <br />
                    <div v-if="form.sendLater" class="send-at" data-cy="send_at">
                      <b-field class="send-at-row">
                        <b-datetimepicker v-model="sendAtPicker" :disabled="!canEdit" required editable mobile-native expanded
                          position="is-top-right" :placeholder="$t('campaigns.dateAndTime')" icon="calendar-clock"
                          :timepicker="{ hourFormat: '24' }" :datetime-formatter="formatDateTime"
                          horizontal-time-picker />
                        <timezone-picker v-model="form.sendTz" :instant="form.sendAtDate" :disabled="!canEdit" />
                      </b-field>
                      <p v-if="form.sendAtDate" class="help send-at-duration">
                        {{ $utils.duration(Date(), form.sendAtDate) }}
                      </p>
                      <p v-if="form.sendAtDate" class="send-at-zones" data-cy="send-at-zones">
                        <template v-for="(z, i) in sendAtZones">
                          <span v-if="i > 0" :key="`s-${z.tz}`" class="sep">&#8203;|&#8203;</span>
                          <span :key="z.tz" class="zone" :data-tz="z.tz">
                            <b v-if="z.kst" class="kst">{{ $t('campaigns.sendTz.kst') }}</b><span v-else class="city">{{ z.city }}</span>
                            <b>{{ z.parts.md }} ({{ $t(`globals.days.${z.parts.wd + 1}`) }}) {{ z.parts.hm }}</b>
                          </span>
                        </template>
                      </p>
                      <p v-if="sendAtBrowserNote" class="send-at-note" data-cy="send-at-browser-note">{{ sendAtBrowserNote }}</p>
                    </div>
                  </div>
                </div>

                <div>
                  <p class="has-text-right">
                    <a href="#" @click.prevent="onShowHeaders" data-cy="btn-headers">
                      <b-icon icon="plus" />{{ $t('settings.smtp.setCustomHeaders') }}
                    </a>
                  </p>
                  <b-field v-if="form.headersStr !== '[]' || isHeadersVisible" label-position="on-border"
                    :message="$t('campaigns.customHeadersHelp')">
                    <b-input v-model="form.headersStr" name="headers" type="textarea"
                      placeholder="[{&quot;X-Custom&quot;: &quot;value&quot;}, {&quot;X-Custom2&quot;: &quot;value&quot;}]"
                      :disabled="!canEdit" />
                  </b-field>
                </div>
                <hr />

                <b-field v-if="isNew">
                  <b-button native-type="submit" type="is-primary" :loading="loading.campaigns || savingCampaign" data-cy="btn-continue">
                    {{ $t('campaigns.continue') }}
                  </b-button>
                </b-field>
              </form>
            </div>
            <div v-if="canManage || canCheckTracking" class="column is-4 is-offset-1">
              <br />
              <tracking-check v-if="canCheckTracking" ref="tracking" :campaign-id="data.id" :payload="trackingPayload" />
              <campaign-test-send v-if="canManage" v-model="form.testEmails" :sample.sync="testSample" :rejected.sync="testRejected"
                :own-unverified="testOwnUnverified" :disabled="isNew" :loading="loading.campaigns" :preview="testPreview"
                @send="() => onSubmit('test')" />
            </div>
          </div>
        </section>
      </b-tab-item><!-- campaign -->

      <b-tab-item :label="$t('campaigns.content')" icon="text" :disabled="isNew" value="content">
        <editor v-if="data.id" v-model="form.content" :id="data.id" :title="data.name" :disabled="!canEdit"
          :templates="templates" :content-types="contentTypes" :list-ids="form.lists.map((l) => l.id)"
          :preview-sample="previewSample" :preview-sample-label="previewSampleLabel" />

        <div class="columns">
          <div class="column is-6">
            <p v-if="!isAttachFieldVisible" class="is-size-6 has-text-grey">
              <a href="#" @click.prevent="onShowAttachField()" data-cy="btn-attach">
                <b-icon icon="file-upload-outline" size="is-small" />
                {{ $t('campaigns.addAttachments') }}
              </a>
            </p>

            <b-field v-if="isAttachFieldVisible" :label="$t('campaigns.attachments')" label-position="on-border"
              expanded data-cy="media">
              <b-taginput v-model="form.media" name="media" ellipsis icon="tag-outline" ref="media" field="filename"
                @focus="onOpenAttach" :disabled="!canEdit" />
            </b-field>
          </div>
          <div class="column has-text-right">
            <a href="https://listmonk.app/docs/templating/#template-expressions" target="_blank"
              rel="noopener noreferer">
              <b-icon icon="code" /> {{ $t('campaigns.templatingRef') }}</a>
            <span v-if="canEdit && form.content.contentType !== 'plain'" class="is-size-6 has-text-grey ml-6">
              <a v-if="form.altbody === null" href="#" @click.prevent="onAddAltBody">
                <b-icon icon="text" size="is-small" /> {{ $t('campaigns.addAltText') }}
              </a>
              <a v-else href="#" @click.prevent="$utils.confirm(null, onRemoveAltBody)">
                <b-icon icon="trash-can-outline" size="is-small" />
                {{ $t('campaigns.removeAltText') }}
              </a>
            </span>
          </div>
        </div>

        <div v-if="canEdit && form.content.contentType !== 'plain'" class="alt-body">
          <b-input v-if="form.altbody !== null" v-model="form.altbody" type="textarea" :disabled="!canEdit" />
        </div>
      </b-tab-item><!-- content -->

      <b-tab-item :label="$t('globals.terms.attribs')" icon="code" value="attribs" :disabled="isNew">
        <section class="wrap">
          <b-field :label="$t('globals.terms.attribs')" :message="$t('campaigns.attribsHelp')"
            label-position="on-border">
            <b-input v-model="form.attribsStr" type="textarea" :disabled="!canEdit" rows="15" data-cy="campaign-attribs" />
          </b-field>
        </section>
      </b-tab-item><!-- attribs -->

      <b-tab-item :label="$t('campaigns.archive')" icon="newspaper-variant-outline" value="archive" :disabled="isNew">
        <section class="wrap">
          <b-notification v-if="replyError" type="is-danger is-light" :closable="false" role="alert">
            {{ replyError }}
          </b-notification>
          <div class="columns">
            <div class="column is-4">
              <b-field :label="$t('campaigns.archiveEnable')" data-cy="btn-archive"
                :message="$t('campaigns.archiveHelp')">
                <div class="columns">
                  <div class="column">
                    <b-switch data-cy="btn-archive" v-model="form.archive" :disabled="!canArchive" />
                  </div>
                  <div class="column is-12">
                    <a :href="`${serverConfig.root_url}/archive/${data.uuid}`" target="_blank" rel="noopener noreferer"
                      :class="{ 'has-text-grey-light': !form.archive }" aria-label="$t('campaigns.archive')">
                      <b-icon icon="link-variant" />
                    </a>
                  </div>
                </div>
              </b-field>
            </div>
            <div class="column is-8">
              <b-field grouped position="is-right">
                <b-field v-if="!canEdit && canArchive">
                  <b-button @click="onUpdateCampaignArchive" :loading="loading.campaigns" type="is-primary"
                    icon-left="content-save-outline" data-cy="btn-save">
                    {{ $t('globals.buttons.saveChanges') }}
                  </b-button>
                </b-field>
              </b-field>
            </div>
          </div>

          <div class="columns">
            <div class="column is-6">
              <b-field :label="$tc('globals.terms.template')" label-position="on-border">
                <b-select :placeholder="$tc('globals.terms.template')" v-model="form.archiveTemplateId" name="template"
                  :disabled="!canArchive || !form.archive || form.content.contentType === 'visual'" required>
                  <template v-for="t in templates">
                    <option v-if="t.type === 'campaign'" :value="t.id" :key="t.id">
                      {{ t.name }}
                    </option>
                  </template>
                </b-select>
              </b-field>
            </div>

            <div class="column is-6">
              <b-field grouped position="is-right">
                <b-field v-if="form.archive && (!this.form.archiveMetaStr || this.form.archiveMetaStr === '{}')">
                  <a class="button is-primary" href="#" @click.prevent="onFillArchiveMeta" aria-label="{}"><b-icon
                      icon="code" /></a>
                </b-field>
                <b-field v-if="form.archive">
                  <b-button @click="onToggleArchivePreview" type="is-primary" icon-left="file-find-outline"
                    data-cy="btn-preview">
                    {{ $t('campaigns.preview') }}
                  </b-button>
                </b-field>
              </b-field>
            </div>
          </div>
          <b-field>
            <b-field :label="$t('campaigns.archiveSlug')" label-position="on-border"
              :message="$t('campaigns.archiveSlugHelp')">
              <b-input :maxlength="200" :ref="'focus'" v-model="form.archiveSlug" name="archive_slug"
                data-cy="archive-slug" :disabled="!canArchive || !form.archive" />
            </b-field>
          </b-field>
          <b-field :label="$t('campaigns.archiveMeta')" :message="$t('campaigns.archiveMetaHelp')"
            label-position="on-border">
            <b-input v-model="form.archiveMetaStr" name="archive_meta" type="textarea" data-cy="archive-meta"
              :disabled="!canArchive || !form.archive" rows="20" />
          </b-field>
        </section>
      </b-tab-item><!-- archive -->
    </b-tabs>

    <b-modal scroll="keep" :aria-modal="true" :active.sync="isAttachModalOpen" :width="900">
      <div class="modal-card content" style="width: auto">
        <section expanded class="modal-card-body">
          <media is-modal @selected="onAttachSelect" />
        </section>
      </div>
    </b-modal>

    <campaign-preview v-if="isPreviewingArchive" @close="onToggleArchivePreview" type="campaign" :id="data.id"
      :archive-meta="form.archiveMetaStr" :title="data.title" :content-type="data.contentType"
      :template-id="form.archiveTemplateId" is-post is-archive />
  </section>
</template>

<script>
import dayjs from 'dayjs';
import htmlToPlainText from 'textversionjs';
import Vue from 'vue';
import { mapState } from 'vuex';

import CampaignPreview from '../components/CampaignPreview.vue';
import CampaignTestSend from '../components/CampaignTestSend.vue';
import CopyText from '../components/CopyText.vue';
import Editor from '../components/Editor.vue';
import ListSelector from '../components/ListSelector.vue';
import AddressCheck from '../components/AddressCheck.vue';
import CampaignFollowupBanner from '../components/CampaignFollowupBanner.vue';
import RecentSendExclude from '../components/RecentSendExclude.vue';
import TimezonePicker from '../components/TimezonePicker.vue';
import Media from './Media.vue';
import SenderAddressInput from '../components/SenderAddressInput.vue';
import TrackingCheck from '../components/TrackingCheck.vue';
import CampaignSendConfirm from '../components/CampaignSendConfirm.vue';
import { isSenderDomain } from '../senders';
import { defaultRecentRule, recentRulePayload } from '../recent-send-exclude';
import {
  browserDiffers, browserZoneName, instantToWall, KST, mergeAttribs, otherZoneLines, splitAttribs, wallToInstant,
} from '../schedule-time';
import {
  newSample, rejectedOf, sampleLabel, samplePayload,
} from '../test-send';
import { trackingToastParts } from '../tracking-check';

export default Vue.extend({
  components: {
    ListSelector,
    Editor,
    Media,
    CopyText,
    CampaignPreview,
    CampaignTestSend,
    SenderAddressInput,
    AddressCheck,
    CampaignFollowupBanner,
    RecentSendExclude,
    TimezonePicker,
    TrackingCheck,
    CampaignSendConfirm,
  },

  data() {
    return {
      contentTypes: Object.freeze({
        richtext: this.$t('campaigns.richText'),
        html: this.$t('campaigns.rawHTML'),
        markdown: this.$t('campaigns.markdown'),
        plain: this.$t('campaigns.plainText'),
        visual: this.$t('campaigns.visual'),
      }),

      isNew: false,
      isFollowup: false,
      isEditing: false,
      isHeadersVisible: false,
      isAttachFieldVisible: false,
      isAttachModalOpen: false,
      isPreviewingArchive: false,
      activeTab: 'campaign',
      replyError: '',
      recentRule: defaultRecentRule(),
      // The window every start, resume and schedule goes through (LST-48).
      isSendVisible: false,
      sendAsk: null,

      savedRecentRule: JSON.stringify({ enabled: false }),
      recentRuleLoaded: false,
      recentRuleError: '',
      createdDraftId: 0,
      // The list IDs the created draft was saved with (the page stays new while its rule fails).
      createdLists: [],
      savingCampaign: false,
      // The list made from the recent-send exclusion result: { id, error }. Whether the server
      // has it as the target is read from the saved campaign (madeSaved), never remembered.
      madeList: null,
      // A leave confirmation is open: a save waiting for its turn must not run until it is answered.
      leaveAsked: false,

      // The test box (LST-41): the sample for addresses that are not subscribers, and the
      // addresses the server refused last time.
      testSample: newSample(),
      testRejected: [],
      testOwnUnverified: false,

      data: {},

      // IDs from ?list_id query param.
      selListIDs: [],

      // Binds form input values.
      form: {
        archiveSlug: null,
        name: '',
        subject: '',
        fromEmail: '',
        fromChoice: null,
        replyTo: '',
        headersStr: '[]',
        headers: [],
        attribsStr: '{}',
        messenger: 'email',
        lists: [],
        tags: [],
        sendAt: null,
        content: {
          contentType: 'richtext',
          body: '',
          bodySource: null,
          templateId: null,
        },
        altbody: null,
        media: [],

        // Parsed Date() version of send_at from the API.
        sendAtDate: null,
        // The time zone the picker is read in (LST-50); kept in the campaign's attribs.
        sendTz: KST,
        sendLater: false,
        archive: false,
        archiveMetaStr: '{}',
        archiveMeta: {},
        testEmails: [],
      },
    };
  },

  methods: {
    // The From a new campaign starts with, as the server decides it: with a
    // From list the entry in serverConfig.from_default, else the app default.
    defaultFrom() {
      if (this.senderPolicyEnabled || this.fromList.length === 0) return this.serverConfig.from_email;
      return this.fromList[this.serverConfig.from_default ?? 0];
    },

    // Remembers which list entry the From stands for (the server says: from_match).
    chooseFrom(email) {
      this.form.fromEmail = email;
      this.form.fromChoice = this.fromList.includes(email) ? email : null;
    },

    // The list entry the server matched to a campaign's From, or null.
    fromChoiceOf(d) {
      return !this.senderPolicyEnabled && d.fromEntry ? d.fromEntry : null;
    },

    onReplyAddresses() {
      if (this.$refs.replyTo?.allowed) this.replyError = '';
    },

    senderFields() {
      return this.senderPolicyEnabled ? { reply_to: this.form.replyTo } : { from_email: this.form.fromEmail };
    },

    async checkReplyTo() {
      if (!this.senderPolicyEnabled) return true;
      await this.$refs.replyTo.refresh();
      if (this.$refs.replyTo.allowed) { this.replyError = ''; return true; }
      this.activeTab = 'campaign';
      this.replyError = this.$refs.replyTo.invalid ? this.$t('senders.invalidEmail')
        : this.$t('senders.unverified', { email: this.form.replyTo });
      return false;
    },
    formatDateTime(s) {
      return dayjs(s).format('YYYY-MM-DD HH:mm');
    },

    onToggleArchivePreview() {
      this.isPreviewingArchive = !this.isPreviewingArchive;
    },

    onAddAltBody() {
      this.form.altbody = htmlToPlainText(this.form.content.body);
    },

    onRemoveAltBody() {
      this.form.altbody = null;
    },

    onShowHeaders() {
      this.isHeadersVisible = !this.isHeadersVisible;
    },

    onShowAttachField() {
      this.isAttachFieldVisible = true;
      this.$nextTick(() => {
        this.$refs.media.focus();
      });
    },

    onOpenAttach() {
      this.isAttachModalOpen = true;
    },

    onAttachSelect(o) {
      if (this.form.media.some((m) => m.id === o.id)) {
        return;
      }

      this.form.media.push(o);
    },

    isUnsaved() {
      return this.recentRuleDirty || this.madeUnsaved || this.data.body !== this.form.content.body
        || this.data.contentType !== this.form.content.contentType;
    },

    onTab(tab) {
      if (tab === 'content' && window.tinymce && window.tinymce.editors.length > 0) {
        this.$nextTick(() => {
          window.tinymce.editors[0].focus();
        });
      }

      // this.$router.replace({ hash: `#${tab}` });
      window.history.replaceState({}, '', `#${tab}`);
    },

    onFillArchiveMeta() {
      const archiveStr = `{"email": "email@domain.com", "name": "${this.$t('globals.fields.name')}", "attribs": {}}`;
      this.form.archiveMetaStr = this.$utils.getPref('campaign.archiveMetaStr') || JSON.stringify(JSON.parse(archiveStr), null, 4);
    },

    // The attribs to send: the JSON tab (an object) plus the chosen send time zone. Returns undefined,
    // after a message, when the JSON tab is not an object. Used by every save, so starting or
    // scheduling straight from the screen keeps the time zone too.
    attribsToSave() {
      let attribs = null;
      if (this.form.attribsStr && this.form.attribsStr.trim()) {
        try {
          attribs = JSON.parse(this.form.attribsStr);
        } catch (e) {
          this.$utils.toast(`${this.$t('subscribers.invalidJSON')}: ${e.toString()}`, 'is-danger', 3000);
          return undefined;
        }
        if (attribs !== null && (typeof attribs !== 'object' || Array.isArray(attribs))) {
          this.$utils.toast(this.$t('subscribers.invalidJSON'), 'is-danger', 3000);
          return undefined;
        }
      }
      const scheduled = !!(this.form.sendLater && this.form.sendAtDate);
      return attribs === null && !scheduled ? null : mergeAttribs(attribs, this.form.sendTz, scheduled);
    },

    onSubmit(typ) {
      if (this.savingCampaign) return;
      if (!this.parseFormJSON()) return;

      switch (typ) {
        case 'create':
          this.createCampaign().catch(() => {});
          break;
        case 'test':
          this.sendTest();
          break;
        default:
          this.updateCampaign().catch(() => {});
          break;
      }
    },

    // Reads the JSON text fields (headers, archive meta, attributes) into the form values
    // that are saved. Every path that saves calls this first: saving without it writes the
    // values of the last save, not what is on the page. False: one of them does not parse.
    parseFormJSON() {
      // Validate custom JSON headers.
      if (this.form.headersStr && this.form.headersStr !== '[]') {
        try {
          this.form.headers = JSON.parse(this.form.headersStr);
        } catch (e) {
          this.$utils.toast(e.toString(), 'is-danger');
          return false;
        }
      } else {
        this.form.headers = [];
      }

      // Validate archive JSON body.
      if (this.form.archive && this.form.archiveMetaStr) {
        try {
          this.form.archiveMeta = JSON.parse(this.form.archiveMetaStr);
        } catch (e) {
          this.$utils.toast(e.toString(), 'is-danger');
          return false;
        }
      }

      // Validate custom JSON attribs.
      const attribs = this.attribsToSave();
      if (attribs === undefined) return false;
      this.form.attribs = attribs;
      return true;
    },

    onFollowupLoaded(info) {
      this.isFollowup = !!info && info.role === 'followup';
    },

    getCampaign(id) {
      return this.$api.getCampaign(id).then(async (data) => {
        this.data = data;
        const { tz, rest } = splitAttribs(data.attribs);
        this.form = {
          ...this.form,
          ...data,
          sendTz: tz,
          headersStr: JSON.stringify(data.headers, null, 4),
          archiveMetaStr: data.archiveMeta ? JSON.stringify(data.archiveMeta, null, 4) : '{}',
          attribsStr: data.attribs ? JSON.stringify(rest, null, 4) : '{}',

          // The structure that is populated by editor input event.
          content: {
            contentType: data.contentType,
            body: data.body,
            bodySource: data.bodySource,
            templateId: data.templateId,
          },
        };
        this.form.fromChoice = this.fromChoiceOf(data);
        this.isAttachFieldVisible = this.form.media.length > 0;

        this.form.media = this.form.media.map((f) => {
          if (!f.id) {
            return { ...f, filename: `❌ ${f.filename}` };
          }
          return f;
        });

        if (data.type === 'regular') {
          try {
            const rule = await this.$api.getRecentRule(id);
            this.recentRule = { ...defaultRecentRule(), ...rule };
            this.savedRecentRule = this.recentRuleKey;
            this.recentRuleLoaded = true;
          } catch {
            this.recentRuleError = this.$t('campaigns.recent.loadError');
          }
        }
      });
    },

    async saveRecentRule(id) {
      if (!this.recentRuleDirty) return;
      const payload = recentRulePayload(this.recentRule);
      try {
        const rule = await this.$api.saveRecentRule(id, payload);
        this.recentRule = { ...defaultRecentRule(), ...rule };
        this.savedRecentRule = this.recentRuleKey;
        this.recentRuleError = '';
      } catch (err) {
        this.recentRuleError = this.$t('campaigns.recent.saveError');
        this.activeTab = 'campaign';
        throw err;
      }
    },

    async sendTest() {
      if (!await this.checkReplyTo()) return false;
      const sample = samplePayload(this.testSample);
      if (sample.error) return false;
      const data = {
        id: this.data.id,
        name: this.form.name,
        subject: this.form.subject,
        lists: this.form.lists.map((l) => l.id),
        ...this.senderFields(),
        messenger: this.form.messenger,
        type: 'regular',
        headers: this.form.headers,
        tags: this.form.tags,
        template_id: this.form.content.templateId,
        content_type: this.form.content.contentType,
        body: this.form.content.body,
        altbody: this.form.content.contentType !== 'plain' ? this.form.altbody : null,
        subscribers: this.form.testEmails,
        media: this.form.media.map((m) => m.id),
      };
      if (sample.payload) data.sample = sample.payload;

      this.testRejected = [];
      // The tracking state of exactly what this test sends (LST-44): the test takes these
      // fields from the form and the rest (the attributes) from the saved campaign.
      const tracking = this.$api.checkCampaignTracking(this.data.id, {
        content_type: data.content_type,
        body: data.body || '',
        template_id: data.template_id || 0,
        messenger: data.messenger,
        name: data.name,
        subject: data.subject,
        headers: data.headers,
        mode: 'test',
        media: data.media,
        ...(data.altbody === null ? {} : { altbody: data.altbody }),
        // The sender fields as the test sends them: with sender domains the server sets
        // the sender from the account of the person sending the test (LST-44 review).
        ...this.senderFields(),
      }).catch(() => null);
      this.$api.testCampaign(data).then(async () => {
        const parts = trackingToastParts(await tracking).map(({ key, params }) => this.$t(key, params));
        if (parts.length) {
          this.$utils.toast([this.$t('campaigns.testSent'), ...parts].join(' · '), 'is-warning', 6000);
        } else {
          this.$utils.toast(this.$t('campaigns.testSent'));
        }
      }).catch((err) => {
        // Refused addresses are marked in the test box itself.
        this.testRejected = rejectedOf(err);
        this.testOwnUnverified = !!err.response?.data?.data?.own_unverified;
        if (this.senderPolicyEnabled && this.testRejected.length === 0) this.replyError = err.response?.data?.message || '';
      });
      return false;
    },

    async createCampaign() {
      const data = {
        archiveSlug: this.form.subject,
        name: this.form.name,
        subject: this.form.subject,
        lists: this.form.lists.map((l) => l.id),
        ...this.senderFields(),
        content_type: this.form.content.contentType,
        messenger: this.form.messenger,
        type: 'regular',
        tags: this.form.tags,
        send_at: this.form.sendLater ? this.form.sendAtDate : null,
        headers: this.form.headers,
        attribs: this.form.attribs,
        media: this.form.media.map((m) => m.id),
      };

      this.savingCampaign = true;
      try {
        // If the draft was created but saving the rule failed, retry on the same draft.
        const d = this.createdDraftId ? await this.$api.updateCampaign(this.createdDraftId, data)
          : await this.$api.createCampaign(data);
        this.createdDraftId = d.id;
        this.createdLists = data.lists;
        await this.saveRecentRule(d.id);
        // The made list is the new campaign's target now (or was not chosen): nothing is pending.
        this.madeList = null;
        this.$router.push({ name: 'campaign', hash: '#content', params: { id: d.id } });
      } finally {
        this.savingCampaign = false;
      }
    },

    // Resolves when no save is running (or after 30 seconds of saving) and no leave confirmation is
    // open, or once the page is gone. A save run while the person is being asked whether to discard
    // would store what they then discard.
    whenFree() {
      return new Promise((resolve) => {
        let ticks = 0;
        const check = () => {
          if (this.savingCampaign) ticks += 1;
          if (this.isDestroyed || ((!this.savingCampaign || ticks > 300) && !this.leaveAsked)) resolve();
          else setTimeout(check, 100);
        };
        check();
      });
    },

    // Whether a save that sent these list IDs saved the made list as the only target.
    sentMadeList(ids) {
      return ids.length === 1 && ids[0] === this.madeList.id;
    },

    // The list made from the exclusion result becomes the campaign's only target. A campaign that
    // exists on the server is saved right away; the list stays even if saving fails.
    async onRecentListMade(result) {
      const { id, name } = result.list;
      // The target changes before anything is awaited, so a save pressed meanwhile carries it.
      this.form.lists = [{ id, name }];
      this.madeList = { id, error: '' };
      this.$api.getLists({ minimal: true, per_page: 'all', status: 'active' }).catch(() => {});
      if (!this.data.id) return;
      try {
        // A save that started before the list existed carries the old target: let it finish, then
        // save the new one (a skipped save would leave the old target on the server).
        await this.whenFree();
        // The person may have left (and discarded) or made another list meanwhile.
        if (this.isDestroyed || !this.madeList || this.madeList.id !== id) return;
        if (!this.parseFormJSON()) throw new Error('invalid form');
        // The result line says it is saved; no second notice.
        if (!await this.updateCampaign(undefined, { quiet: true })) throw new Error('not saved');
      } catch (err) {
        // A campaign saved with the made list whose rule alone was not saved has its target: the
        // rule's own error says what failed.
        if (!this.isDestroyed && this.madeList && this.madeList.id === id && !this.madeSaved) {
          this.madeList = { id, error: this.$t('campaigns.recent.listSaveError') };
        }
      }
    },

    // quiet: no notice (the start shows it after its questions).
    async updateCampaign(typ, { quiet = false } = {}) {
      if (this.savingCampaign) return undefined;
      const attribs = this.attribsToSave();
      if (attribs === undefined) throw new Error('invalid attribs');
      this.form.attribs = attribs;
      const data = {
        archive_slug: this.form.archiveSlug,
        name: this.form.name,
        subject: this.form.subject,
        lists: this.form.lists.map((l) => l.id),
        ...this.senderFields(),
        messenger: this.form.messenger,
        type: 'regular',
        tags: this.form.tags,
        send_at: this.form.sendLater ? this.form.sendAtDate : null,
        headers: this.form.headers,
        attribs: this.form.attribs,
        template_id: this.form.content.templateId,
        content_type: this.form.content.contentType,
        body: this.form.content.body,
        body_source: this.form.content.bodySource,
        altbody: this.form.content.contentType !== 'plain' ? this.form.altbody : null,
        archive: this.form.archive,
        archive_template_id: this.form.archiveTemplateId,
        archive_meta: this.form.archiveMeta,
        media: this.form.media.map((m) => m.id),
      };

      let typMsg = 'globals.messages.updated';
      if (typ === 'start') {
        typMsg = 'campaigns.started';
      }

      if (!this.form.sendAtDate) {
        this.form.sendLater = false;
      }

      // Save and propagate errors before starting or scheduling a campaign.
      this.savingCampaign = true;
      try {
        const d = await this.$api.updateCampaign(this.data.id, data).catch((err) => {
          if (this.senderPolicyEnabled) this.replyError = err.response?.data?.message || '';
          throw err;
        });
        this.data = d;
        this.form.fromEmail = d.fromEmail;
        this.form.fromChoice = this.fromChoiceOf(d);
        this.form.replyTo = d.replyTo;
        this.form.archiveSlug = d.archiveSlug;
        this.form.attribsStr = d.attribs ? JSON.stringify(splitAttribs(d.attribs).rest, null, 4) : '{}';
        // The target is saved now, whatever happens to the rule below.
        if (this.madeList && this.sentMadeList(data.lists)) this.madeList = { ...this.madeList, error: '' };
        await this.saveRecentRule(d.id);
        if (!quiet) this.$utils.toast(this.$t(typMsg, { name: d.name }));
        // Saving can change what the check reads beyond the page (the sender of the
        // person who saves, merged headers): check again (LST-44).
        if (this.$refs.tracking) this.$refs.tracking.run();
        return d;
      } finally {
        this.savingCampaign = false;
      }
    },

    onUpdateCampaignArchive() {
      if (this.isEditing && this.canEdit) {
        return;
      }

      const data = {
        archive: this.form.archive,
        archive_template_id: this.form.archiveTemplateId,
        archive_meta: JSON.parse(this.form.archiveMetaStr),
        archive_slug: this.form.archiveSlug,
      };

      this.$api.updateCampaignArchive(this.data.id, data).then((d) => {
        this.form.archiveSlug = d.archiveSlug;
      });
    },

    // Starts or schedule a campaign.
    //
    // Saves the page first, then opens the pre-send window (LST-48) on the saved campaign:
    // the window reads from the server what will really be sent (the people, the sender,
    // the tracking and the other checks) and, when confirmed, only changes the status. The
    // server refuses the change when the campaign was edited after the window read it.
    // Cancelling the window leaves the edits saved (approved LST-48 design, A1 note).
    async startCampaign() {
      if (this.savingCampaign || (!this.canStart && !this.canSchedule)) {
        return;
      }

      if (!await this.checkReplyTo()) return;
      if (!this.parseFormJSON()) return;
      const status = this.canStart ? 'running' : 'scheduled';
      let saved;
      try {
        // The save notice is not shown over the window; closing the window shows it.
        saved = await this.updateCampaign(undefined, { quiet: true });
      } catch (e) {
        return;
      }
      if (!saved) return;

      // The window shows the campaign as saved and, when confirmed, only changes its status.
      this.sendAsk = {
        id: saved.id, name: saved.name, status, handled: false, busy: false,
      };
      this.isSendVisible = true;
    },

    onSendDone() {
      if (this.sendAsk) this.sendAsk.handled = true;
      this.$router.push({ name: 'campaigns' });
    },

    onSendFailed(err) {
      // The error is shown by the window.
      if (this.sendAsk) this.sendAsk.handled = true;
      if (this.senderPolicyEnabled) this.replyError = err.response?.data?.message || '';
    },

    // The window was closed without sending: say that the edits were saved before it opened.
    onSendClosed() {
      const ask = this.sendAsk;
      this.sendAsk = null;
      if (ask && !ask.handled) this.$utils.toast(this.$t('globals.messages.updated', { name: ask.name }));
    },

    unscheduleCampaign() {
      this.$api.changeCampaignStatus(this.data.id, 'draft').then((d) => {
        this.data = d;
      });
    },
  },

  computed: {
    ...mapState(['serverConfig', 'loading', 'lists', 'templates', 'profile']),

    // The picker works on local fields, so it gets the moment as a clock time in the chosen zone.
    sendAtPicker: {
      get() {
        return this.form.sendAtDate ? instantToWall(this.form.sendAtDate, this.form.sendTz) : null;
      },
      set(v) {
        this.form.sendAtDate = v ? wallToInstant(v, this.form.sendTz) : null;
      },
    },

    // KST first, then the frequent zones, for the same moment.
    sendAtZones() {
      return this.form.sendAtDate ? otherZoneLines(this.form.sendAtDate, this.form.sendTz) : [];
    },

    sendAtBrowserNote() {
      const t = this.form.sendAtDate;
      if (!t || !browserDiffers(t, this.form.sendTz)) return '';
      return this.$t('campaigns.sendTz.browserNote', { zone: browserZoneName(), time: dayjs(t).format('MM/DD HH:mm') });
    },

    // What the tracking check renders: everything a template can read from the campaign,
    // as the form has it now. Starting saves exactly these first. JSON that is still being
    // typed (headers, attributes) is left out until it parses, and the saved value counts.
    trackingPayload() {
      const json = (str) => { try { return JSON.parse(str); } catch (e) { return undefined; } };
      const headers = this.form.headersStr && this.form.headersStr !== '[]' ? json(this.form.headersStr) : [];
      // Empty or null attributes are saved as none: say so, or the saved ones would count.
      let attribs = this.form.attribsStr && this.form.attribsStr.trim() ? (json(this.form.attribsStr) ?? {}) : {};
      // Saving adds the send time zone (LST-50), which the attributes tab does not show.
      if (attribs && typeof attribs === 'object' && !Array.isArray(attribs)) {
        attribs = mergeAttribs(attribs, this.form.sendTz, !!(this.form.sendLater && this.form.sendAtDate));
      }
      const altbody = this.form.content.contentType !== 'plain' ? this.form.altbody : null;
      // As saving reads it: the archive metadata on the page when it parses.
      let archiveMeta = this.form.archive && this.form.archiveMetaStr ? json(this.form.archiveMetaStr) : undefined;
      if (archiveMeta === undefined) archiveMeta = this.form.archiveMeta || undefined;
      return {
        content_type: this.form.content.contentType,
        body: this.form.content.body || '',
        template_id: this.form.content.templateId || 0,
        messenger: this.form.messenger,
        name: this.form.name,
        subject: this.form.subject,
        tags: this.form.tags,
        ...(Array.isArray(headers) ? { headers } : {}),
        ...(attribs && typeof attribs === 'object' && !Array.isArray(attribs) ? { attribs } : {}),
        ...(altbody === null || altbody === undefined ? {} : { altbody }),
        ...this.senderFields(),
        // What the send will be: templates can read the status (LST-44 review).
        status: this.form.sendLater && this.form.sendAtDate ? 'scheduled' : 'running',
        send_at: this.form.sendLater ? this.form.sendAtDate : null,
        // The rest of what saving sends that a template can read (LST-44 review).
        archive: this.form.archive,
        archive_slug: this.form.archiveSlug || null,
        archive_template_id: this.form.archiveTemplateId || null,
        ...(archiveMeta === undefined ? {} : { archive_meta: archiveMeta }),
        body_source: this.form.content.bodySource || null,
        media: this.form.media.map((m) => m.id),
      };
    },

    // The pre-send tracking check is for a saved campaign that can still be edited.
    canCheckTracking() {
      return this.isEditing && !!this.data.id && this.canEdit;
    },

    canShowRecentRule() {
      return this.isNew || this.data.type === 'regular';
    },
    // The campaign has been started (LST-65): the rule panel says how many people it left out.
    recentStarted() {
      return !!this.data.startedAt || ['running', 'paused', 'finished', 'cancelled'].includes(this.data.status);
    },
    // An opt-in campaign also mails people: its audience (the unconfirmed subscribers of the
    // double opt-in lists) is checked too.
    canShowAddressCheck() {
      return this.canShowRecentRule || this.data.type === 'optin';
    },
    recentListIds() {
      return this.form.lists.map(({ id }) => id).filter((id) => id > 0);
    },
    // The server has the made list as the campaign's only target (read from the saved campaign).
    // A new campaign whose draft was created but whose rule failed reads the draft's lists.
    madeSaved() {
      let saved = [];
      if (this.data.id) saved = (this.data.lists || []).map(({ id }) => id);
      else if (this.createdDraftId) saved = this.createdLists;
      return !!this.madeList && saved.length === 1 && saved[0] === this.madeList.id;
    },
    // The form targets the made list, but the server does not have it yet.
    madeUnsaved() {
      return !!this.madeList && this.form.lists.length === 1 && this.form.lists[0].id === this.madeList.id
        && !this.madeSaved;
    },
    madeListView() {
      return this.madeList && { ...this.madeList, saved: this.madeSaved };
    },
    recentRuleKey() {
      return JSON.stringify(recentRulePayload(this.recentRule));
    },
    recentRuleDirty() {
      return this.canShowRecentRule && this.recentRuleKey !== this.savedRecentRule;
    },

    fromList() { return this.serverConfig.from_addresses || []; },
    // The From list replaces free input only while it can be edited and the
    // account-sender feature (which decides the From itself) is off.
    useFromSelect() { return !this.senderPolicyEnabled && this.fromList.length > 0 && this.canEdit; },
    fromSelected: {
      get() { return this.form.fromChoice || null; },
      set(v) { this.chooseFrom(v); },
    },
    // Warns about a saved From that the server did not match to the list (or,
    // with no list, called a placeholder). Gone once another entry is picked.
    fromWarning() {
      const email = (this.data.fromEmail || '').trim();
      if (!this.isEditing || !this.canEdit || !email || this.senderPolicyEnabled) return '';
      if (this.fromList.length === 0) {
        return this.data.fromPlaceholder && this.form.fromEmail === this.data.fromEmail
          ? this.$t('campaigns.fromPlaceholderWarnFree', { email }) : '';
      }
      if (this.form.fromChoice) return '';
      return this.$t(this.data.fromPlaceholder ? 'campaigns.fromPlaceholderWarn' : 'campaigns.fromNotInListWarn', { email });
    },

    senderPolicyEnabled() { return (this.serverConfig.sender_domains || []).length > 0; },

    // The sample of the test box as the preview takes it, and its name for the preview header.
    previewSample() {
      const { payload } = samplePayload(this.testSample);
      return payload ? JSON.stringify(payload) : '';
    },
    previewSampleLabel() {
      const label = sampleLabel(this.testSample);
      if (label.kind === 'default') return '';
      return this.$t(`campaigns.testSend.previewLabel.${label.kind}`, { name: label.name || '—' });
    },
    testPreview() {
      return {
        id: this.data.id || 0,
        title: this.data.name || '',
        contentType: this.form.content.contentType,
        templateId: this.form.content.templateId,
        body: this.form.content.body,
        sample: this.previewSample,
        sampleLabel: this.previewSampleLabel,
      };
    },
    usesAccountFrom() { return isSenderDomain(this.profile.email, this.serverConfig.sender_domains); },
    resolvedFrom() {
      if (this.isEditing && !['draft', 'scheduled'].includes(this.data.status)) return this.data.fromEmail;
      return this.serverConfig.resolved_from;
    },

    canManage() {
      return this.$can('campaigns:manage_all', 'campaigns:manage');
    },

    canSend() {
      return this.$can('campaigns:send');
    },

    // The target can change only while the campaign has not started (not while paused).
    canChangeLists() {
      return this.isNew || this.data.status === 'draft' || this.data.status === 'scheduled';
    },

    canEdit() {
      return this.isNew
        || this.data.status === 'draft' || this.data.status === 'scheduled' || this.data.status === 'paused';
    },

    canSchedule() {
      return (this.data.status === 'draft' || this.data.status === 'paused') && (this.form.sendLater && this.form.sendAtDate);
    },

    canUnSchedule() {
      return this.data.status === 'scheduled';
    },

    canStart() {
      return (this.data.status === 'draft' || this.data.status === 'paused') && !this.form.sendLater;
    },

    canArchive() {
      return this.data.status !== 'cancelled' && this.data.type !== 'optin';
    },

    selectedLists() {
      if (this.selListIDs.length === 0 || !this.lists.results) {
        return [];
      }

      return this.lists.results.filter((l) => this.selListIDs.indexOf(l.id) > -1);
    },

    emailMessengers() {
      return ['email', ...this.serverConfig.messengers.filter((m) => m.startsWith('email-'))];
    },

    otherMessengers() {
      return this.serverConfig.messengers.filter((m) => m !== 'email' && !m.startsWith('email-'));
    },
  },

  beforeRouteLeave(to, from, next) {
    if (this.isUnsaved()) {
      this.leaveAsked = true;
      this.$utils.confirm(this.$t('globals.messages.confirmDiscard'), () => next(true), () => {
        this.leaveAsked = false;
      });
      return;
    }
    next(true);
  },

  watch: {
    'form.replyTo': function clearReplyError() { this.replyError = ''; },
    // The ?list_id lists fill the target once, when the lists are known. A later refresh of the
    // lists (making a list from the exclusion result reads them again) must not put them back.
    selectedLists(lists) {
      if (lists.length === 0) return;
      this.form.lists = lists;
      this.selListIDs = [];
    },

    // The stored status decides what the check reads besides the page: a draft is sent
    // with the sender of the person who starts it, a scheduled campaign with the stored
    // one. When it changes on this page (unschedule), check again (LST-44).
    'data.status': function onStatus(now, before) {
      if (!before || now === before) return;
      this.$nextTick(() => {
        if (this.$refs.tracking) this.$refs.tracking.run();
      });
    },

    // eslint-disable-next-line func-names
    'data.sendAt': function () {
      if (this.data.sendAt !== null) {
        this.form.sendLater = true;
        this.form.sendAtDate = dayjs(this.data.sendAt).toDate();
      } else {
        this.form.sendLater = false;
        this.form.sendAtDate = null;
      }
    },
  },

  mounted() {
    window.onbeforeunload = () => this.isUnsaved() || null;

    // Refresh the server config: the computed sender may have changed with the
    // profile, and another session may have replaced the From list since this
    // page was loaded. A new campaign then takes the refreshed default.
    // Fill default form fields.
    this.chooseFrom(this.defaultFrom());
    const initialFrom = this.form.fromEmail;

    const cfgReady = this.$api.getServerConfig();
    cfgReady.then(() => {
      if (!this.isNew || this.senderPolicyEnabled) return;
      // Replace a From that came from the old config: the untouched default,
      // or one that the refreshed list no longer has.
      if (this.form.fromEmail === initialFrom
        || (this.fromList.length > 0 && !this.fromList.includes(this.form.fromEmail))) {
        this.chooseFrom(this.defaultFrom());
      }
    });
    if (this.senderPolicyEnabled && !this.usesAccountFrom) this.form.replyTo = this.serverConfig.default_reply_to;

    // New campaign.
    const { id } = this.$route.params;
    if (id === 'new') {
      this.isNew = true;
      this.recentRuleLoaded = true;

      if (this.$route.query.list_id) {
        // Multiple list_id query params.
        let strIds = [];
        if (typeof this.$route.query.list_id === 'object') {
          strIds = this.$route.query.list_id;
        } else {
          strIds = [this.$route.query.list_id];
        }

        this.selListIDs = strIds.map((v) => parseInt(v, 10));
      }
    } else {
      const intID = parseInt(id, 10);
      if (intID <= 0 || Number.isNaN(intID)) {
        this.$utils.toast(this.$t('campaigns.invalid'));
        return;
      }

      this.isEditing = true;
    }

    // Get templates list.
    this.$api.getTemplates().then((data) => {
      if (data.length > 0) {
        if (!this.form.templateId) {
          const tpl = data.find((i) => i.isDefault === true);
          this.form.templateId = tpl.id;
        }
      }
    });

    // Fetch campaign.
    if (this.isEditing) {
      this.getCampaign(id).then(() => {
        if (this.$route.hash !== '') {
          this.activeTab = this.$route.hash.replace('#', '');
        }
      });
    } else {
      this.form.messenger = 'email';
    }

    this.$nextTick(() => {
      this.$refs.focus.focus();
    });

    this.$events.$on('campaign.update', () => {
      this.onSubmit('update');
    });
  },

  beforeDestroy() {
    this.$events.$off('campaign.update');
  },
});
</script>

<style scoped>
.send-at-row { margin-bottom: 0; }
/* The column can be narrow on a wide screen too (tablets, small windows): the input keeps room
   for the date and time, and the zone button goes under it rather than squeezing it. */
.send-at { container-type: inline-size; }
.send-at-row ::v-deep .has-addons { flex-wrap: wrap; row-gap: .5rem; }
.send-at-row ::v-deep .datepicker { flex: 1 1 12rem; min-width: min(12rem, 100%); }
/* 25rem: the input's 12rem and the button's 12.5rem side by side. */
@container (max-width: 25rem) {
  .send-at-row ::v-deep .datepicker { flex-basis: 100%; }
  .send-at-row ::v-deep .field.has-addons .control.tzp { flex: 0 1 auto; max-width: 100%; }
  .send-at-row ::v-deep .field.has-addons .control.tzp .tzp-button { width: auto; max-width: 100%; min-width: 0; border-radius: 4px; }
  .send-at-row ::v-deep .tzp-panel { left: 0; right: auto; }
}
@media (max-width: 520px) {
  /* The zone button goes under the input instead of past the edge of the screen. */
  .send-at-row ::v-deep .datepicker { flex: 1 1 100%; min-width: 0; }
  .send-at-row ::v-deep .field.has-addons .control.tzp .tzp-button { border-radius: 4px; }
}
.send-at-duration { margin-top: .5rem; }
.send-at-zones { font-size: .8125rem; color: #6b7280; margin-top: .25rem; line-height: 1.7; }
.send-at-zones .zone { white-space: nowrap; }
.send-at-zones .zone b { color: #374151; font-weight: 600; }
.send-at-zones .zone b.kst { color: #1d4ed8; }
.send-at-zones .zone > :first-child { margin-right: .3em; }
.send-at-zones .sep { margin: 0 .4rem; color: #c4c9d1; }
.send-at-note { margin-top: .5rem; padding: .5rem .75rem; background: #fff8e1; border: 1px solid #f1dc9a; border-radius: 6px; font-size: .8125rem; color: #614600; line-height: 1.5; }
.sender-readonly {
  background: #f7f8fa; border: 1px solid #ddd; border-radius: 4px; padding: 12px; font-size: 14px;
  display: flex; gap: 8px; align-items: center; justify-content: space-between; flex-wrap: wrap; overflow-wrap: anywhere;
}
.sender-readonly + .help { line-height: 1.7; }
</style>
