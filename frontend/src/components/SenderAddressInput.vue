<template>
  <div class="sender-address-input">
    <b-field :label="label" label-position="on-border" :type="invalid ? 'is-danger' : ''">
      <b-autocomplete :value="value" @input="$emit('input', $event)" :data="suggestions"
        :name="name" :disabled="disabled" :placeholder="$t('campaigns.replyToPlaceholder')"
        :maxlength="2000" open-on-focus keep-first />
    </b-field>
    <div class="sender-actions" aria-live="polite">
      <b-tag v-if="invalid" type="is-danger is-light">{{ $t('senders.invalidStatus') }}</b-tag>
      <b-tag v-else-if="internal" type="is-success is-light">✓ {{ $t('senders.ready') }}</b-tag>
      <b-tag v-else-if="verified" type="is-success is-light">✓ {{ $t('senders.verified') }}</b-tag>
      <b-tag v-else-if="mailbox" type="is-warning is-light">{{ $t('senders.pending') }}</b-tag>
      <b-button v-if="mailbox && !internal && !verified && canVerify" size="is-small"
        type="is-link is-outlined" :loading="sending" :disabled="disabled || !enabled || cooldown > 0"
        @click="sendVerification">
        {{ cooldown > 0 ? $t('senders.resendIn', { seconds: cooldown })
          : $t(address ? 'senders.resend' : 'senders.sendVerification') }}
      </b-button>
      <b-button v-if="mailbox && !internal && canVerify" size="is-small" type="is-text"
        :loading="refreshing" @click="refresh">
        {{ $t('senders.refresh') }}
      </b-button>
    </div>
    <p v-if="sentTo === mailbox && !verified" class="help has-text-success" role="status">
      {{ $t('senders.sent') }}
    </p>
    <p class="help" :class="{ 'is-danger': invalid }">
      {{ invalid ? $t('senders.invalidEmail') : !value.trim() ? $t('campaigns.replyToEmptyHelp')
        : !allowed ? $t('campaigns.replyToPendingHelp') : $t('campaigns.replyToHelp') }}
    </p>
    <p v-if="mailbox && !internal && !verified" class="help">{{ $t('senders.linkHelp') }}</p>
  </div>
</template>

<script>
import { mapState } from 'vuex';
import { senderMailbox, isSenderDomain } from '../senders';

export default {
  props: {
    value: { type: String, default: '' },
    label: { type: String, required: true },
    name: { type: String, default: 'reply_to' },
    disabled: { type: Boolean, default: false },
  },
  data() {
    return {
      addresses: [], sending: false, refreshing: false, now: Date.now(), timer: null, sentTo: '',
    };
  },
  computed: {
    ...mapState(['serverConfig']),
    canVerify() { return this.$can('campaigns:manage', 'campaigns:manage_all'); },
    enabled() { return (this.serverConfig.sender_domains || []).length > 0; },
    mailbox() { return senderMailbox(this.value); },
    invalid() { return !!this.value.trim() && !this.mailbox; },
    internal() { return isSenderDomain(this.value, this.serverConfig.sender_domains); },
    address() { return this.addresses.find((a) => a.email === this.mailbox); },
    verified() { return !!this.address && this.address.status === 'verified'; },
    allowed() { return !this.value.trim() || this.internal || this.verified; },
    cooldown() {
      if (!this.address || this.verified) return 0;
      return Math.max(0, Math.ceil((new Date(this.address.lastSentAt).getTime() + 60000 - this.now) / 1000));
    },
    suggestions() {
      return this.addresses.filter((a) => a.status === 'verified'
        && a.email.includes(this.value.trim().toLowerCase())).map((a) => a.email);
    },
  },
  methods: {
    async refresh() {
      if (!this.canVerify) return;
      this.refreshing = true;
      try {
        this.addresses = await this.$api.getSenderAddresses();
        this.now = Date.now();
        this.$emit('addresses', this.addresses);
      } finally {
        this.refreshing = false;
      }
    },
    async sendVerification() {
      this.sending = true;
      try {
        await this.$api.requestSenderAddress({ email: this.value });
        this.sentTo = this.mailbox;
      } finally {
        try { await this.refresh(); } finally { this.sending = false; }
      }
    },
  },
  mounted() {
    this.refresh();
    this.timer = window.setInterval(() => { this.now = Date.now(); }, 1000);
  },
  beforeDestroy() { window.clearInterval(this.timer); },
};
</script>

<style scoped>
.sender-address-input { margin-top: 30px; margin-bottom: 28px; }
.sender-address-input .field { margin-bottom: 0; }
.sender-actions { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; margin-top: 10px; }
.sender-actions .button { height: auto; min-height: 32px; }
.help { line-height: 1.7; }
</style>
