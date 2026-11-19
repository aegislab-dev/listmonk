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
              <b-button expanded @click="() => onSubmit('update')" :loading="loading.campaigns || savingCampaign || startBusy" type="is-primary"
                icon-left="content-save-outline" data-cy="btn-save" aria-keyshortcuts="ctrl+s">
                <span class="has-kbd">{{ $t('globals.buttons.saveChanges') }} <span class="kbd">Ctrl+S</span></span>
              </b-button>
            </b-field>
            <b-field expanded v-if="canSend && canStart">
              <b-button expanded @click="startCampaign" :loading="loading.campaigns || savingCampaign || startBusy" type="is-primary"
                icon-left="rocket-launch-outline" data-cy="btn-start">
                {{ $t('campaigns.start') }}
              </b-button>
            </b-field>
            <b-field expanded v-if="canSend && canSchedule">
              <b-button expanded @click="startCampaign" :loading="loading.campaigns || savingCampaign || startBusy" type="is-primary"
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
                <b-field v-else :label="$t('campaigns.fromAddress')" label-position="on-border">
                  <b-input :maxlength="200" v-model="form.fromEmail" name="from_email" :disabled="!canEdit"
                    :placeholder="$t('campaigns.fromAddressPlaceholder')" required />
                </b-field>

                <list-selector v-model="form.lists" :selected="form.lists" :all="lists.results" :disabled="!canEdit"
                  :label="$t('globals.terms.lists')" :placeholder="$t('campaigns.sendToLists')" />

                <recent-send-exclude v-if="canShowRecentRule" v-model="recentRule" :campaign-id="data.id || createdDraftId"
                  :list-ids="recentListIds" :campaign-type="data.type" :disabled="!canManage || !canEdit || !recentRuleLoaded || savingCampaign" />
                <p v-if="recentRuleError" class="help is-danger mb-4" role="alert">{{ recentRuleError }}</p>

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
                    <b-field v-if="form.sendLater" data-cy="send_at"
                      :message="form.sendAtDate ? $utils.duration(Date(), form.sendAtDate) : ''">
                      <b-datetimepicker v-model="form.sendAtDate" :disabled="!canEdit" required editable mobile-native
                        position="is-top-right" :placeholder="$t('campaigns.dateAndTime')" icon="calendar-clock"
                        :timepicker="{ hourFormat: '24' }" :datetime-formatter="formatDateTime"
                        horizontal-time-picker />
                    </b-field>
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
              <div v-if="canManage" class="box">
                <h3 class="title is-size-6">
                  {{ $t('campaigns.sendTest') }}
                </h3>
                <b-field :message="$t('campaigns.sendTestHelp')">
                  <b-taginput v-model="form.testEmails" :before-adding="$utils.validateEmail" :disabled="isNew" ellipsis
                    icon="email-outline" :placeholder="$t('campaigns.testEmails')" />
                </b-field>
                <p class="help mb-4">{{ $t('campaigns.personalize.testHelp') }}</p>
                <b-field>
                  <b-button @click="() => onSubmit('test')" :loading="loading.campaigns" :disabled="isNew"
                    type="is-primary" icon-left="email-outline">
                    {{ $t('campaigns.send') }}
                  </b-button>
                </b-field>
              </div>
            </div>
          </div>
        </section>
      </b-tab-item><!-- campaign -->

      <b-tab-item :label="$t('campaigns.content')" icon="text" :disabled="isNew" value="content">
        <editor v-if="data.id" v-model="form.content" :id="data.id" :title="data.name" :disabled="!canEdit"
          :templates="templates" :content-types="contentTypes" :list-ids="form.lists.map((l) => l.id)" />

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
            <b-input v-model="form.attribsStr" type="textarea" :disabled="!canEdit" rows="15" />
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
import CopyText from '../components/CopyText.vue';
import Editor from '../components/Editor.vue';
import ListSelector from '../components/ListSelector.vue';
import RecentSendExclude from '../components/RecentSendExclude.vue';
import Media from './Media.vue';
import SenderAddressInput from '../components/SenderAddressInput.vue';
import TrackingCheck from '../components/TrackingCheck.vue';
import { isSenderDomain } from '../senders';
import { defaultRecentRule, recentRulePayload } from '../recent-send-exclude';
import { trackingConfirmLines, trackingToastParts, warningsCovered } from '../tracking-check';

export default Vue.extend({
  components: {
    ListSelector,
    Editor,
    Media,
    CopyText,
    CampaignPreview,
    SenderAddressInput,
    RecentSendExclude,
    TrackingCheck,
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
      isEditing: false,
      isHeadersVisible: false,
      isAttachFieldVisible: false,
      isAttachModalOpen: false,
      isPreviewingArchive: false,
      activeTab: 'campaign',
      replyError: '',
      recentRule: defaultRecentRule(),
      savedRecentRule: JSON.stringify({ enabled: false }),
      recentRuleLoaded: false,
      recentRuleError: '',
      createdDraftId: 0,
      savingCampaign: false,

      // A start/schedule in progress (LST-44): its number, and its open confirmation.
      startingCampaign: false,
      startBusy: false,
      startSeq: 0,
      startAsk: null,
      isDestroyed: false,

      data: {},

      // IDs from ?list_id query param.
      selListIDs: [],

      // Binds form input values.
      form: {
        archiveSlug: null,
        name: '',
        subject: '',
        fromEmail: '',
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
        sendLater: false,
        archive: false,
        archiveMetaStr: '{}',
        archiveMeta: {},
        testEmails: [],
      },
    };
  },

  methods: {
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
      return this.recentRuleDirty || this.data.body !== this.form.content.body
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

    onSubmit(typ) {
      // While a start is being checked and confirmed, a save would change what was checked.
      if (this.savingCampaign || this.startingCampaign) return;
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
      let attribs = null;
      if (this.form.attribsStr && this.form.attribsStr.trim()) {
        try {
          attribs = JSON.parse(this.form.attribsStr);
        } catch (e) {
          this.$utils.toast(
            `${this.$t('subscribers.invalidJSON')}: ${e.toString()}`,
            'is-danger',

            3000,
          );
          return false;
        }
      }
      this.form.attribs = attribs;
      return true;
    },

    getCampaign(id) {
      return this.$api.getCampaign(id).then(async (data) => {
        this.data = data;
        this.form = {
          ...this.form,
          ...data,
          headersStr: JSON.stringify(data.headers, null, 4),
          archiveMetaStr: data.archiveMeta ? JSON.stringify(data.archiveMeta, null, 4) : '{}',
          attribsStr: data.attribs ? JSON.stringify(data.attribs, null, 4) : '{}',

          // The structure that is populated by editor input event.
          content: {
            contentType: data.contentType,
            body: data.body,
            bodySource: data.bodySource,
            templateId: data.templateId,
          },
        };
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
        if (this.senderPolicyEnabled) this.replyError = err.response?.data?.message || '';
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
        await this.saveRecentRule(d.id);
        this.$router.push({ name: 'campaign', hash: '#content', params: { id: d.id } });
      } finally {
        this.savingCampaign = false;
      }
    },

    // quiet: no notice (the start shows it after its questions).
    async updateCampaign(typ, { quiet = false } = {}) {
      if (this.savingCampaign) return undefined;
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
        this.form.replyTo = d.replyTo;
        this.form.archiveSlug = d.archiveSlug;
        this.form.attribsStr = d.attribs ? JSON.stringify(d.attribs, null, 4) : '{}';
        await this.saveRecentRule(d.id);
        if (!quiet) this.$utils.toast(this.$t(typMsg, { name: d.name }));
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
    // The tracking warning (LST-44) is about what will really be sent, and cancelling does
    // nothing. So: check what is on the page, ask; only after the confirmation save, check
    // the saved campaign again and start. If the saved campaign warns about something that
    // was not confirmed (an edit made while the first check was on its way, headers that
    // the server merges on save, a save from another tab), ask again with what was found.
    // Every step first makes sure that this page still shows this campaign and that this
    // start is the current one: a page that was left or a newer start drops it.
    async startCampaign() {
      if (this.savingCampaign || this.startingCampaign || (!this.canStart && !this.canSchedule)) {
        return;
      }

      const { id } = this.data;
      this.startSeq += 1;
      const seq = this.startSeq;
      const current = () => !this.isDestroyed && this.startSeq === seq && this.data.id === id;
      // Start or schedule: decided by the page as it is when asking, and again by what
      // was saved (the send time may be switched off while the check is on its way).
      const action = () => {
        if (this.canStart) return 'running';
        return this.canSchedule ? 'scheduled' : null;
      };
      // Only the requests show as loading on the buttons; an open question does not.
      const busy = async (p) => {
        this.startBusy = true;
        try {
          return await p;
        } finally {
          if (this.startSeq === seq) this.startBusy = false;
        }
      };
      const check = (payload) => busy(this.$api.checkCampaignTracking(id, payload).catch(() => null)
        .then((r) => trackingConfirmLines(r)));

      this.startingCampaign = true;
      let saved = null;
      try {
        if (!await busy(this.checkReplyTo()) || !current()) return;
        if (!this.parseFormJSON()) return;

        // The box checks the same values, so it shows what the question says.
        if (this.$refs.tracking) this.$refs.tracking.run();
        let lines = await check(this.trackingPayload);
        let status = action();
        for (;;) {
          if (!current() || !status) return;
          // eslint-disable-next-line no-await-in-loop
          if (!await this.askStart(lines, status) || !current()) return;

          if (!saved) {
            // Saves the page as it is now (it may have been edited while the check was
            // on its way); the check of the saved campaign below covers the difference.
            if (!this.parseFormJSON()) return;
            try {
              // eslint-disable-next-line no-await-in-loop
              saved = await this.updateCampaign(undefined, { quiet: true });
            } catch (e) {
              return;
            }
            if (!saved || !current()) return;
          }

          const after = action();
          if (!after) return;
          // eslint-disable-next-line no-await-in-loop
          const now = await check({ status: after });
          if (!current()) return;
          if (after === status && warningsCovered(lines, now)) break;
          lines = now;
          status = after;
        }

        await busy(this.$api.changeCampaignStatus(id, status).then(() => {
          if (current()) this.$router.push({ name: 'campaigns' });
        }).catch((err) => {
          if (this.senderPolicyEnabled && current()) this.replyError = err.response?.data?.message || '';
        }));
      } finally {
        if (this.startSeq === seq) this.startingCampaign = false;
        // The save notice waits until the questions are answered: it does not belong
        // over the confirmation (approved design v1).
        if (saved) this.$utils.toast(this.$t('globals.messages.updated', { name: saved.name }));
      }
    },

    // The start/schedule confirmation, with the tracking warnings when there are any.
    // Resolves true on confirm, false on cancel or when the page goes away.
    askStart(lines, status) {
      return new Promise((resolve) => {
        const done = (ok) => {
          if (this.startAsk && this.startAsk.resolve === done) this.startAsk = null;
          resolve(ok);
        };
        const warnings = lines.map(({ key, params }) => this.$t(key, params));
        const msg = this.recentRule.enabled
          ? `${this.$t('globals.messages.confirm')}\n${this.$t('campaigns.recent.sendNotice')}` : null;
        const dialog = warnings.length
          ? this.$utils.confirmWithWarnings(
            msg,
            warnings,
            this.$t(`campaigns.tracking.confirm.${status === 'running' ? 'start' : 'schedule'}`),
            () => done(true),
            () => done(false),
          )
          : this.$utils.confirm(msg, () => done(true), () => done(false));
        this.startAsk = { dialog, resolve: done };
      });
    },

    unscheduleCampaign() {
      this.$api.changeCampaignStatus(this.data.id, 'draft').then((d) => {
        this.data = d;
      });
    },
  },

  computed: {
    ...mapState(['serverConfig', 'loading', 'lists', 'templates', 'profile']),

    // What the tracking check renders: everything a template can read from the campaign,
    // as the form has it now. Starting saves exactly these first. JSON that is still being
    // typed (headers, attributes) is left out until it parses, and the saved value counts.
    trackingPayload() {
      const json = (str) => { try { return JSON.parse(str); } catch (e) { return undefined; } };
      const headers = this.form.headersStr && this.form.headersStr !== '[]' ? json(this.form.headersStr) : [];
      // Empty or null attributes are saved as none: say so, or the saved ones would count.
      const attribs = this.form.attribsStr && this.form.attribsStr.trim() ? (json(this.form.attribsStr) ?? {}) : {};
      const altbody = this.form.content.contentType !== 'plain' ? this.form.altbody : null;
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
      };
    },

    // The pre-send tracking check is for a saved campaign that can still be edited.
    canCheckTracking() {
      return this.isEditing && !!this.data.id && this.canEdit;
    },

    canShowRecentRule() {
      return this.isNew || this.data.type === 'regular';
    },
    recentListIds() {
      return this.form.lists.map(({ id }) => id).filter((id) => id > 0);
    },
    recentRuleKey() {
      return JSON.stringify(recentRulePayload(this.recentRule));
    },
    recentRuleDirty() {
      return this.canShowRecentRule && this.recentRuleKey !== this.savedRecentRule;
    },

    senderPolicyEnabled() { return (this.serverConfig.sender_domains || []).length > 0; },
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
      this.$utils.confirm(this.$t('globals.messages.confirmDiscard'), () => next(true));
      return;
    }
    next(true);
  },

  watch: {
    'form.replyTo': function clearReplyError() { this.replyError = ''; },
    selectedLists() {
      this.form.lists = this.selectedLists;
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

    // Refresh the server-computed sender after a profile change in this session.
    if (this.senderPolicyEnabled) this.$api.getServerConfig();

    // Fill default form fields.
    this.form.fromEmail = this.serverConfig.from_email;
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

    // A start that is still being checked or confirmed belongs to this page only.
    this.isDestroyed = true;
    this.startSeq += 1;
    if (this.startAsk) {
      const { dialog, resolve } = this.startAsk;
      if (dialog) dialog.close();
      resolve(false);
    }
  },
});
</script>

<style scoped>
.sender-readonly {
  background: #f7f8fa; border: 1px solid #ddd; border-radius: 4px; padding: 12px; font-size: 14px;
  display: flex; gap: 8px; align-items: center; justify-content: space-between; flex-wrap: wrap; overflow-wrap: anywhere;
}
.sender-readonly + .help { line-height: 1.7; }
</style>
