<template>
  <span v-if="campaign.own" class="st-actions">
    <router-link :to="{ name: 'campaignAnalytics', query: { id: campaign.id } }" data-cy="result-analytics">
      {{ $t('stats.analyze') }}
    </router-link>
    <a v-if="canClone" href="#" data-cy="result-clone" @click.prevent="openClone">{{ $t('globals.buttons.clone') }}</a>
    <a v-if="canFollowup" href="#" data-cy="result-followup" @click.prevent="followup = true">{{ $t('stats.followupAction') }}</a>
    <router-link v-if="showOpen && canRead" :to="{ name: 'campaign', params: { id: campaign.id } }">{{ $t('stats.openCamp') }}</router-link>
    <b-modal v-if="original" has-modal-card scroll="keep" :aria-modal="true" active :width="520" trap-focus
      :can-cancel="['escape', 'outside']" @close="original = null">
      <campaign-clone-form :campaign="original" @close="original = null" @clone="copy" />
    </b-modal>
    <campaign-reaction-list v-if="followup" :campaign-id="campaign.id" prefer-followup @close="followup = false" />
  </span>
</template>

<script>
import CampaignCloneForm from './CampaignCloneForm.vue';
import CampaignReactionList from './CampaignReactionList.vue';
import cloneCampaign from '../campaign-clone';

export default {
  components: { CampaignCloneForm, CampaignReactionList },
  props: {
    campaign: { type: Object, required: true },
    showOpen: { type: Boolean, default: false },
  },
  data() { return { original: null, followup: false }; },
  computed: {
    canRead() { return this.$can('campaigns:get', 'campaigns:get_all'); },
    canClone() { return this.canRead && this.$can('campaigns:manage', 'campaigns:manage_all'); },
    canFollowup() { return this.canClone && this.$canCreateList() && this.campaign.status === 'finished'; },
  },
  methods: {
    async openClone() {
      this.original = await this.$api.getCampaign(this.campaign.id).catch(() => null);
    },
    async copy({ name, listIds }) {
      const { id } = this.original;
      this.original = null;
      const result = await cloneCampaign(this.$api, this.$utils.getDate(), id, name, listIds).catch(() => null);
      if (result) this.$router.push({ name: 'campaign', params: { id: result.id } });
    },
  },
};
</script>

<style scoped>
.st-actions { display: inline-flex; flex-wrap: wrap; gap: 0.35em 0.7em; }
</style>
