<template>
  <div v-if="info && info.role !== 'none'" class="followup-banner" data-cy="followup-banner" role="status">
    <template v-if="info.role === 'followup'">
      <p class="followup-line">
        <strong>↳
          <router-link v-if="info.parent.name" :to="{ name: 'campaign', params: { id: info.parent.id } }"
            data-cy="followup-parent-link">
            {{ f('bannerOf', { id: info.parent.id, name: info.parent.name }) }}
          </router-link>
          <span v-else>{{ f('bannerOf', { id: info.parent.id, name: '—' }) }}</span>
        </strong>
        <template v-if="conditionText"> · {{ f('condition') }} <strong>{{ conditionText }}</strong></template>
        <template v-if="info.list"> · {{ f('listCount', { count: num(info.list.count) }) }}</template>
      </p>
      <p class="followup-line">
        {{ f('bannerRecheck') }}
        <template v-if="info.pending">
          <strong data-cy="followup-pending">{{ f('pending', { count: num(pendingTotal) }) }}</strong>
        </template>
        <template v-if="info.recheck && removedTotal > 0">
          <strong data-cy="followup-removed">{{ f('removed', removedParams) }}</strong>
        </template>
      </p>
      <p v-if="madeAt" class="followup-line followup-muted">{{ madeAt }}</p>
      <p v-if="info.listsChanged" class="followup-line followup-warn" data-cy="followup-lists-changed">
        {{ f('listsChanged') }}
      </p>
    </template>
    <p v-else class="followup-line" data-cy="followup-child-link">
      {{ f('hasFollowup') }}
      <router-link :to="{ name: 'campaign', params: { id: info.followup.id } }">
        <strong>{{ f('open', { id: info.followup.id }) }} 「{{ info.followup.name }}」</strong>
      </router-link>
      · {{ $t(`campaigns.status.${info.followup.status}`) }}
    </p>
  </div>
</template>

<script>
import Vue from 'vue';
import dayjs from 'dayjs';
import { conditionWords } from '../followup';

// The link of a campaign to the campaign it follows, or to its own follow-up (LST-47).
export default Vue.extend({
  name: 'CampaignFollowupBanner',
  props: {
    campaignId: { type: Number, required: true },
    status: { type: String, default: '' },
    // Saving the campaign changes it (its lists, for one), so the banner reads again.
    updatedAt: { type: String, default: '' },
  },
  data() {
    return { info: null };
  },
  computed: {
    conditionText() {
      return conditionWords(this.info?.condition, (key) => this.$t(key));
    },
    pendingTotal() {
      const p = this.info?.pending;
      return p ? p.reacted + p.optedOut + p.blocked + (p.unknown || 0) : 0;
    },
    removedTotal() {
      const r = this.info?.recheck;
      return r ? r.reacted + r.optedOut + r.blocked + (r.unknown || 0) : 0;
    },

    removedParams() {
      const r = this.info.recheck;
      return {
        count: this.num(this.removedTotal), reacted: this.num(r.reacted), optedOut: this.num(r.optedOut), blocked: this.num(r.blocked),
        unknown: this.num(r.unknown || 0),
      };
    },
    madeAt() {
      const { counts } = this.info;
      if (!counts || !this.info.createdAt) return '';
      return this.f('madeAt', {
        time: dayjs(this.info.createdAt).format('YYYY-MM-DD HH:mm'),
        received: this.num(counts.received),
        matched: this.num(counts.matched),
        condition: this.conditionText,
      });
    },
  },
  watch: {
    campaignId() { this.load(); },
    // A status change (scheduled, started) changes what the banner can say.
    status() { this.load(); },
    updatedAt() { this.load(); },
  },
  methods: {
    f(key, params) { return this.$t(`campaigns.followup.${key}`, params); },
    num(value) { return this.$utils.formatNumber(value); },
    async load() {
      try {
        this.info = await this.$api.getCampaignFollowup(this.campaignId);
      } catch (e) {
        // The banner is an extra; the campaign page works without it.
        this.info = null;
      }
      this.$emit('loaded', this.info);
    },
  },
  mounted() {
    this.load();
  },
});
</script>

<style scoped>
.followup-banner { margin: 0 0 14px; padding: 10px 14px; background: #eef4fc; border-left: 3px solid #2364c6; border-radius: 3px; font-size: .88rem; color: #2c4a73; line-height: 1.55; }
.followup-banner .followup-line { margin: 0; }
.followup-banner .followup-muted { color: #5b6b82; }
.followup-banner .followup-warn { margin-top: 4px; padding: 4px 8px; background: #fff7e5; border-left: 3px solid #da9c1c; color: #724908; }
</style>
