<template>
  <div class="items">
    <b-field :message="$t('settings.businessFooter.help')">
      <b-switch v-model="data.enabled" name="business_footer.enabled">
        {{ $t('settings.businessFooter.enabled') }}
      </b-switch>
    </b-field>

    <p class="block">{{ $t('settings.businessFooter.requiredHelp') }}</p>

    <b-field :label="$t('settings.businessFooter.companyName')" label-for="business-company-name">
      <b-input id="business-company-name" v-model="data.company_name" :required="data.enabled" />
    </b-field>

    <div class="columns">
      <div class="column is-half">
        <b-field :label="$t('settings.businessFooter.representative')" label-for="business-representative"
          :message="$t('settings.businessFooter.optional')">
          <b-input id="business-representative" v-model="data.representative" />
        </b-field>
      </div>
      <div class="column is-half">
        <b-field :label="$t('settings.businessFooter.registrationNo')" label-for="business-registration-no"
          :message="$t('settings.businessFooter.optional')">
          <b-input id="business-registration-no" v-model="data.registration_no" />
        </b-field>
      </div>
    </div>

    <b-field :label="$t('settings.businessFooter.address')" label-for="business-address">
      <b-input id="business-address" v-model="data.address" :required="data.enabled" />
    </b-field>

    <div class="columns">
      <div class="column is-half">
        <b-field :label="$t('settings.businessFooter.phone')" label-for="business-phone">
          <b-input id="business-phone" v-model="data.phone" type="tel" :required="data.enabled" />
        </b-field>
      </div>
      <div class="column is-half">
        <b-field :label="$t('settings.businessFooter.email')" label-for="business-email">
          <b-input id="business-email" v-model="data.email" :required="data.enabled" />
        </b-field>
      </div>
    </div>

    <b-field :label="$t('settings.businessFooter.extra')" label-for="business-extra"
      :message="$t('settings.businessFooter.extraHelp')">
      <b-input id="business-extra" v-model="data.extra" type="textarea" rows="3" />
    </b-field>

    <p class="block is-size-7">{{ $t('settings.businessFooter.duplicateHelp') }}</p>
    <p class="block is-size-7">{{ $t('settings.businessFooter.scopeHelp') }}</p>
    <p class="block is-size-7">{{ $t('settings.businessFooter.restartHelp') }}</p>
  </div>
</template>

<script>
import Vue from 'vue';

export default Vue.extend({
  props: {
    form: { type: Object, default: () => ({}) },
  },

  data() {
    return { data: this.form['axm.business_footer'] };
  },
});
</script>
