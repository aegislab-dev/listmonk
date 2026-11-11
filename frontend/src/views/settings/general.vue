<template>
  <div class="items">
    <b-field :label="$t('settings.general.siteName')" label-position="on-border">
      <b-input v-model="data['app.site_name']" name="app.site_name" :label="$t('settings.general.siteName')"
        :maxlength="300" required />
    </b-field>

    <b-field :label="$t('settings.general.rootURL')" label-position="on-border"
      :message="$t('settings.general.rootURLHelp')">
      <b-input v-model="data['app.root_url']" name="app.root_url" placeholder="https://listmonk.yoursite.com"
        :maxlength="300" required type="url" pattern="https?://.*" />
    </b-field>

    <b-field :label="$t('settings.general.logoURL')" label-position="on-border"
      :message="$t('settings.general.logoURLHelp')">
      <b-input v-model="data['app.logo_url']" name="app.logo_url" placeholder="https://listmonk.yoursite.com/logo.png"
        :maxlength="300" type="url" pattern="https?://.*" />
    </b-field>
    <b-field :label="$t('settings.general.faviconURL')" label-position="on-border"
      :message="$t('settings.general.faviconURLHelp')">
      <b-input v-model="data['app.favicon_url']" name="app.favicon_url"
        placeholder="https://listmonk.yoursite.com/favicon.png" :maxlength="300" type="url" pattern="https?://.*" />
    </b-field>

    <hr />
    <b-field :label="$t('settings.general.fromEmail')" label-position="on-border"
      :message="$t('settings.general.fromEmailHelp')">
      <b-input v-model="data['app.from_email']" name="app.from_email"
        placeholder="Listmonk <noreply@listmonk.yoursite.com>" pattern="((.+?)\s)?<(.+?)@(.+?)>" :maxlength="300" />
    </b-field>
    <section class="sender-settings">
      <hr />
      <h3 class="is-size-5 has-text-weight-semibold">{{ $t('settings.general.senderTitle') }}</h3>
      <p class="help mb-5">{{ $t('settings.general.senderHelp') }}</p>
      <b-field :label="$t('settings.general.senderDomains')" label-position="on-border"
        :message="$t('settings.general.senderDomainsHelp')">
        <b-taginput v-model="data['axm.sender_domains']" name="axm.sender_domains"
          :disabled="!$can('settings:manage')" :placeholder="$t('settings.general.senderDomainsPlaceholder')" />
      </b-field>
      <b-field :label="$t('settings.general.defaultFrom')" label-position="on-border"
        :message="$t('settings.general.defaultFromHelp')">
        <b-input v-model="data['axm.default_from']" name="axm.default_from" :maxlength="2000"
          :disabled="!$can('settings:manage')" />
      </b-field>
      <sender-address-input ref="defaultReplyTo" v-model="data['axm.default_reply_to']"
        name="axm.default_reply_to" :label="$t('settings.general.defaultReplyTo')"
        :disabled="!$can('settings:manage')" @addresses="addresses = $event">
        <template #help="{ verified, invalid }">
          <p class="help" :class="{ 'is-danger': invalid }">
            {{ invalid ? $t('senders.invalidEmail') : $t(verified
              ? 'settings.general.defaultReplyToVerifiedHelp' : 'settings.general.defaultReplyToHelp') }}
          </p>
        </template>
      </sender-address-input>
      <template v-if="canManageCampaigns">
        <h4 class="has-text-weight-semibold mt-5 mb-2">{{ $t('senders.addresses') }}</h4>
        <p class="help mb-3">{{ $t('senders.addressesHelp') }}</p>
        <p v-if="addresses.length === 0" class="help">{{ $t('senders.empty') }}</p>
        <div v-for="address in addresses" :key="address.id" class="sender-address">
          <div>
            <strong>{{ address.email }}</strong>
            <b-tag :type="address.status === 'verified' ? 'is-success is-light' : 'is-warning is-light'">
              {{ $t(address.status === 'verified' ? 'senders.verified' : 'senders.pending') }}
            </b-tag>
            <p class="help">
              {{ $t('senders.requestedBy') }}: {{ address.requesterName || '—' }} ·
              {{ $t(address.status === 'verified' ? 'senders.verifiedAt' : 'senders.requestedAt') }}:
              {{ formatAddressTime(address.status === 'verified' ? address.verifiedAt : address.lastSentAt) }}
            </p>
          </div>
          <b-button v-if="$can('settings:manage') || address.requestedBy === profile.id" size="is-small"
            type="is-danger is-outlined" :loading="deleting === address.id" @click="deleteAddress(address)">
            {{ $t('globals.buttons.delete') }}
          </b-button>
        </div>
        <p class="help">{{ $t('senders.inUse') }}</p>
      </template>
      <p class="help mt-4">{{ $t('settings.general.senderSaveHelp') }}</p>
      <hr />
    </section>
    <b-field :label="$t('settings.general.adminNotifEmails')" label-position="on-border"
      :message="$t('settings.general.adminNotifEmailsHelp')">
      <b-taginput v-model="data['app.notify_emails']" name="app.notify_emails"
        :before-adding="(v) => v.match(/(.+?)@(.+?)/)" placeholder="you@yoursite.com" />
    </b-field>

    <hr />

    <div>
      <h2 class="is-size-4 mb-5">
        {{ $tc('globals.terms.subscriptions', 2) }}
      </h2>
      <b-field :message="$t('settings.general.enablePublicSubPageHelp')">
        <b-switch v-model="data['app.enable_public_subscription_page']" name="app.enable_public_subscription_page">
          {{ $t('settings.general.enablePublicSubPage') }}
        </b-switch>
      </b-field>
      <b-field :message="$t('settings.general.sendOptinConfirmHelp')">
        <b-switch v-model="data['app.send_optin_confirmation']" name="app.send_optin_confirmation">
          {{ $t('settings.general.sendOptinConfirm') }}
        </b-switch>
      </b-field>
      <b-field :message="$t('settings.general.showOptinPageHelp')">
        <b-switch v-model="data['app.show_optin_page']" name="app.show_optin_page">
          {{ $t('settings.general.showOptinPage') }}
        </b-switch>
      </b-field>
    </div>
    <hr />

    <div>
      <h2 class="is-size-4 mb-5">
        {{ $t('campaigns.archive') }}
      </h2>
      <b-field :message="$t('settings.general.enablePublicArchiveHelp')">
        <b-switch v-model="data['app.enable_public_archive']" name="app.enable_public_archive">
          {{ $t('settings.general.enablePublicArchive') }}
        </b-switch>
      </b-field>
      <b-field :message="$t('settings.general.enablePublicArchiveRSSContentHelp')">
        <b-switch v-model="data['app.enable_public_archive_rss_content']" name="app.enable_public_archive_rss_content">
          {{ $t('settings.general.enablePublicArchiveRSSContent') }}
        </b-switch>
      </b-field>
    </div>

    <hr />
    <b-field :message="$t('settings.general.checkUpdatesHelp')">
      <b-switch v-model="data['app.check_updates']" name="app.check_updates">
        {{ $t('settings.general.checkUpdates') }}
      </b-switch>
    </b-field>

    <hr />
    <b-field :label="$t('settings.general.language')" label-position="on-border" :addons="false">
      <b-select v-model="data['app.lang']" name="app.lang">
        <option v-for="l in serverConfig.langs" :key="l.code" :value="l.code">
          {{ l.name }}
        </option>
      </b-select>
      <p class="mt-2">
        <a href="https://listmonk.app/docs/i18n/#additional-language-packs" target="_blank" rel="noopener noreferer">{{
          $t('globals.buttons.more') }} &rarr;</a>
      </p>
    </b-field>
  </div>
</template>

<script>
import dayjs from 'dayjs';
import Vue from 'vue';
import { mapState } from 'vuex';
import SenderAddressInput from '../../components/SenderAddressInput.vue';

export default Vue.extend({
  components: { SenderAddressInput },
  props: {
    form: {
      type: Object, default: () => { },
    },
  },

  data() {
    return {
      data: this.form,
      addresses: [],
      deleting: null,
    };
  },

  methods: {
    formatAddressTime(value) {
      return value ? dayjs(value).format('YYYY-MM-DD HH:mm') : '—';
    },

    deleteAddress(address) {
      this.$utils.confirm(null, async () => {
        this.deleting = address.id;
        try {
          await this.$api.deleteSenderAddress(address.id);
          await this.$refs.defaultReplyTo.refresh();
        } finally {
          this.deleting = null;
        }
      });
    },
  },

  computed: {
    ...mapState(['serverConfig', 'loading', 'profile']),
    canManageCampaigns() { return this.$can('campaigns:manage', 'campaigns:manage_all'); },
  },

});
</script>

<style scoped>
.sender-settings .help { line-height: 1.7; }
.sender-settings .field { margin-top: 28px; margin-bottom: 28px; }
.sender-address {
  border: 1px solid #ddd; padding: 16px; border-radius: 4px; display: flex; gap: 16px;
  align-items: center; justify-content: space-between; flex-wrap: wrap; margin: 12px 0;
}
.sender-address strong { overflow-wrap: anywhere; }
.sender-address .tag { margin: 4px 0; }
</style>
