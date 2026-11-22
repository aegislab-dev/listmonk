<template>
  <form @submit.prevent="onSubmit">
    <div class="modal-card content" style="width: auto">
      <header class="modal-card-head">
        <b-tag v-if="isEditing" :class="[data.status, 'is-pulled-right']">
          {{ $t(`subscribers.status.${data.status}`) }}
        </b-tag>
        <h4 v-if="isEditing">
          {{ data.name }}
        </h4>
        <h4 v-else>
          {{ $t('subscribers.newSubscriber') }}
        </h4>

        <p v-if="isEditing" class="has-text-grey is-size-7">
          {{ $t('globals.fields.id') }}: <span data-cy="id"><copy-text :text="`${data.id}`" /></span>
          {{ $t('globals.fields.uuid') }}: <copy-text :text="data.uuid" />
        </p>
      </header>

      <section expanded class="modal-card-body">
        <div v-if="profileLocked" class="notification is-warning is-light locked-notice" role="note" data-cy="locked-notice">
          <p class="locked-title">
            <svg class="lock-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
              <path d="M6 11h12v9H6zM8.5 11V7.5a3.5 3.5 0 0 1 7 0V11" />
            </svg>
            {{ $t('teams.lockedFormTitle') }}
          </p>
          <p>{{ $t('teams.lockedForm') }}</p>
          <b-taglist v-if="data.otherTeams && data.otherTeams.length" data-cy="other-teams">
            <b-tag v-for="t in data.otherTeams" :key="t.team">
              {{ $t('teams.otherTeamLists', { team: t.team || $t('teams.noTeam'), num: t.lists }) }}
            </b-tag>
          </b-taglist>
        </div>

        <b-field :label="$t('subscribers.email')" label-position="on-border"
          :type="addrError ? 'is-danger' : ''" :message="addrError">
          <b-input :maxlength="200" v-model="form.email" name="email" :ref="'focus'"
            :placeholder="$t('subscribers.email')" required :disabled="profileLocked" @input="addrError = ''" />
        </b-field>
        <p v-if="addrSuggest" class="addr-suggest" data-cy="addr-suggest">
          <b-button size="is-small" type="is-primary" outlined data-cy="addr-suggest-apply" @click="applySuggest">
            {{ $t('subscribers.addr.apply', { domain: addrSuggestDomain }) }}
          </b-button>
        </p>

        <div class="columns">
          <div class="column is-8">
            <b-field :label="$t('globals.fields.name')" label-position="on-border">
              <b-input :maxlength="200" v-model="form.name" name="name" :placeholder="$t('globals.fields.name')"
                :disabled="profileLocked" />
            </b-field>
          </div>
          <div class="column is-4">
            <b-field :label="$t('globals.fields.status')" label-position="on-border"
              :message="statusHelp">
              <b-select v-model="form.status" name="status" :placeholder="$t('globals.fields.status')" required
                expanded :disabled="unblockLocked || profileLocked" data-cy="sub-status">
                <option value="enabled">
                  {{ $t('subscribers.status.enabled') }}
                </option>
                <option value="blocklisted">
                  {{ $t('subscribers.status.blocklisted') }}
                </option>
              </b-select>
            </b-field>
          </div>
        </div>

        <div v-if="isEditing && !profileLocked && data.otherTeams && data.otherTeams.length" class="other-teams-box" data-cy="other-teams">
          <p class="label">{{ $t('teams.otherTeams') }}</p>
          <b-taglist>
            <b-tag v-for="t in data.otherTeams" :key="t.team" type="is-info is-light">
              {{ $t('teams.otherTeamLists', { team: t.team || $t('teams.noTeam'), num: t.lists }) }}
            </b-tag>
          </b-taglist>
          <p class="help">{{ $t('teams.otherTeamsHelp') }}</p>
        </div>

        <b-tabs type="is-boxed" :animated="false">
          <b-tab-item :label="$t('globals.terms.lists')" label-position="on-border">
            <list-selector :label="$t('subscribers.lists')" :placeholder="$t('subscribers.listsPlaceholder')"
              :message="$t('subscribers.listsHelp')" v-model="form.lists" :selected="form.lists" :all="(lists.results || []).filter(l => !l.axmFilter)" />
            <div class="columns">
              <div class="column is-7">
                <b-field :message="$t('subscribers.preconfirmHelp')">
                  <b-checkbox v-model="form.preconfirm" :native-value="true" :disabled="!hasOptinList">
                    {{ $t('subscribers.preconfirm') }}
                  </b-checkbox>
                </b-field>
              </div>
              <div v-if="$can('subscribers:manage') && isEditing" class="column is-5 has-text-right">
                <a href="#" @click.prevent="sendOptinConfirmation" :class="{ 'is-disabled': !hasOptinList }">
                  <b-icon icon="email-outline" size="is-small" />
                  {{ $t('subscribers.sendOptinConfirm') }}</a>
              </div>
            </div>
          </b-tab-item><!-- lists -->

          <b-tab-item :label="`${$tc('globals.terms.subscriptions', 2)} (${data.lists ? data.lists.length : 0})`"
            label-position="on-border" :disabled="!data.lists || data.lists.length === 0">
            <template v-if="data.lists">
              <b-table :data="data.lists" hoverable default-sort="createdAt" class="subscriptions">
                <b-table-column v-slot="props" field="name" :label="$tc('globals.terms.list', 1)">
                  <div>
                    <router-link v-if="!props.row.restricted" :to="`/lists/${props.row.id}`">
                      {{ props.row.name }}
                    </router-link>
                    <span v-else class="has-text-grey-light is-italic">{{ props.row.name }}</span>
                    <br />
                    <b-tag :class="props.row.optin" :data-cy="`optin-${props.row.optin}`">
                      <b-icon :icon="props.row.optin === 'double' ? 'account-check-outline' : 'account-off-outline'"
                        size="is-small" />
                      {{ ' ' }}
                      {{ $t(`lists.optins.${props.row.optin}`) }}
                    </b-tag>{{ ' ' }}
                  </div>
                </b-table-column>

                <b-table-column v-slot="props" field="status" cell-class="status" :label="$t('globals.fields.status')">
                  <b-tag :class="`status-${props.row.subscriptionStatus}`">
                    {{ $t(`subscribers.status.${props.row.subscriptionStatus}`) }}
                  </b-tag>
                  <template v-if="props.row.optin === 'double' && props.row.subscriptionMeta.optinIp">
                    <br /><span class="is-size-7">{{ props.row.subscriptionMeta.optinIp }}</span>
                  </template>
                </b-table-column>

                <b-table-column v-slot="props" field="createdAt" :label="$t('globals.fields.createdAt')">
                  {{ $utils.niceDate(props.row.subscriptionCreatedAt, true) }}
                </b-table-column>

                <b-table-column v-slot="props" field="updatedAt" :label="$t('globals.fields.updatedAt')">
                  {{ $utils.niceDate(props.row.subscriptionCreatedAt, true) }}
                </b-table-column>
              </b-table>
            </template>
          </b-tab-item><!-- subscriptions -->

          <b-tab-item :label="`${$t('globals.terms.bounces')} (${bounces.length})`" class="bounces"
            :disabled="bounces.length === 0">
            <a href="#" class="is-size-6 is-pulled-right" disabed="true" @click.prevent="deleteBounces"
              v-if="isBounceVisible && !profileLocked">
              <b-icon icon="trash-can-outline" />
              {{ $t('globals.buttons.delete') }}
            </a>

            <b-table :data="bounces" hoverable default-sort="createdAt" class="bounces">
              <b-table-column field="campaign" :label="$tc('globals.terms.campaign', 1)" v-slot="props">
                <div v-if="props.row.campaign">
                  <router-link :to="{ name: 'bounces', query: { campaign_id: props.row.campaign.id } }">
                    {{ props.row.campaign.name }}
                  </router-link>
                </div>
              </b-table-column>

              <b-table-column field="createdAt" :label="$t('globals.fields.createdAt')" v-slot="props">
                {{ $utils.niceDate(props.row.createdAt, true) }}
              </b-table-column>

              <b-table-column field="action" :label="$t('globals.fields.type')" v-slot="props">
                <span class="is-pulled-right">
                  <a href="#" @click.prevent="toggleMeta(props.row.id)">
                    {{ props.row.source }}
                    <b-icon :icon="visibleMeta[props.row.id] ? 'arrow-up' : 'arrow-down'" />
                  </a>
                </span>
                <span class="is-clearfix" />
                <pre v-if="visibleMeta[props.row.id]">{{ props.row.meta }}</pre>
              </b-table-column>
            </b-table>
          </b-tab-item><!-- bounces -->

          <b-tab-item :label="$t('subscribers.activity')" class="activity" :disabled="!isEditing">
            <subscriber-activity v-if="isEditing && data.id" :subscriber-id="data.id" />
          </b-tab-item><!-- activity -->
        </b-tabs>

        <b-field :message="$t('subscribers.attribsHelp') + ' ' + egAttribs" class="mt-6">
          <div>
            <h5>{{ $t('globals.terms.attribs') }}</h5>
            <b-input v-model="form.strAttribs" name="attribs" type="textarea" :disabled="profileLocked" />
            <a href="https://listmonk.app/docs/concepts" target="_blank" rel="noopener noreferrer" class="is-size-7">
              {{ $t('globals.buttons.learnMore') }} <b-icon icon="link-variant" size="is-small" />
            </a>
          </div>
        </b-field>
      </section>
      <footer class="modal-card-foot has-text-right">
        <b-button @click="$parent.close()">
          {{ $t('globals.buttons.close') }}
        </b-button>
        <b-button v-if="$can('subscribers:manage')" native-type="submit" type="is-primary"
          :loading="loading.subscribers">
          {{ $t('globals.buttons.save') }}
        </b-button>
      </footer>
    </div>
  </form>
</template>

<script>
import Vue from 'vue';
import { mapState } from 'vuex';
import ListSelector from '../components/ListSelector.vue';
import CopyText from '../components/CopyText.vue';
import SubscriberActivity from '../components/SubscriberActivity.vue';

export default Vue.extend({
  components: {
    ListSelector,
    CopyText,
    SubscriberActivity,
  },

  props: {
    data: {
      type: Object,
      default: () => ({ lists: [] }),
    },
    isEditing: Boolean,
  },

  data() {
    return {
      // Binds form input values. This is populated by subscriber props passed
      // from the parent component in mounted().
      form: {
        lists: [],
        strAttribs: '{}',
        status: 'enabled',
        preconfirm: false,
      },
      // An address rule refused the save (LST-53): the words, and the corrected address for a
      // misspelled domain. Nothing is changed until the person presses the button.
      addrError: '',
      addrSuggest: '',
      isBounceVisible: false,
      bounces: [],
      visibleMeta: {},

      egAttribs: '{"job": "developer", "location": "Mars", "has_rocket": true}',
    };
  },

  methods: {
    // Shows a refusal by the address rules under the address field. Any other error is a toast.
    saveFailed(err) {
      const res = err && err.response;
      const msg = (res && res.data && res.data.message) || String(err);
      if (res && res.headers && res.headers['x-axm-address-rule']) {
        this.addrError = msg;
        this.addrSuggest = res.headers['x-axm-suggest'] || '';
        return;
      }
      this.$utils.toast(msg, 'is-danger');
    },

    applySuggest() {
      this.form.email = this.addrSuggest;
      this.addrSuggest = '';
      this.addrError = '';
    },

    toggleBounces() {
      this.isBounceVisible = !this.isBounceVisible;
    },

    toggleMeta(id) {
      let v = false;
      if (!this.visibleMeta[id]) {
        v = true;
      }
      Vue.set(this.visibleMeta, id, v);
    },

    deleteBounces(sub) {
      this.$utils.confirm(
        null,
        () => {
          this.$api.deleteSubscriberBounces(this.form.id).then(() => {
            this.getBounces();
            this.$utils.toast(this.$t('globals.messages.deleted', { name: sub.name }));
          });
        },
      );
    },

    getBounces() {
      this.$api.getSubscriberBounces(this.form.id).then((data) => {
        this.bounces = data;
      });
    },

    onSubmit() {
      if (this.isEditing) {
        this.updateSubscriber();
        return;
      }

      this.createSubscriber();
    },

    createSubscriber() {
      let attribs = {};
      if (this.form.strAttribs) {
        attribs = this.validateAttribs(this.form.strAttribs);
        if (!attribs) {
          return;
        }
      }

      const data = {
        email: this.form.email,
        name: this.form.name,
        status: this.form.status,
        attribs,
        preconfirm_subscriptions: this.form.preconfirm,

        // List IDs.
        lists: this.form.lists.map((l) => l.id),
      };

      this.$api.createSubscriber(data).then((d) => {
        this.$emit('finished');
        this.$parent.close();
        this.$utils.toast(this.$t('globals.messages.created', { name: d.name }));
      }).catch(this.saveFailed);
    },

    updateSubscriber() {
      // A locked profile is not sent at all: only the lists are saved, so nothing the form
      // shows of the profile can count as a change.
      if (this.profileLocked) {
        this.$api.updateSubscriberLists(this.form.id, {
          lists: this.form.lists.map((l) => l.id),
          preconfirm_subscriptions: this.form.preconfirm,
        }).then((d) => {
          this.$emit('finished');
          this.$parent.close();
          this.$utils.toast(this.$t('globals.messages.updated', { name: d.name }));
        });
        return;
      }

      let attribs = {};
      if (this.form.strAttribs) {
        attribs = this.validateAttribs(this.form.strAttribs);
        if (!attribs) {
          return;
        }
      }

      const data = {
        id: this.form.id,
        email: this.form.email,
        name: this.form.name,
        status: this.form.status,
        preconfirm_subscriptions: this.form.preconfirm,
        attribs,

        // List IDs.
        lists: this.form.lists.map((l) => l.id),
      };

      this.$api.updateSubscriber(data).then((d) => {
        this.$emit('finished');
        this.$parent.close();
        this.$utils.toast(this.$t('globals.messages.updated', { name: d.name }));
      }).catch(this.saveFailed);
    },

    sendOptinConfirmation() {
      this.$api.sendSubscriberOptin(this.form.id).then(() => {
        this.$utils.toast(this.$t('subscribers.sentOptinConfirm'));
      });
    },

    validateAttribs(str) {
      // Parse and validate attributes JSON.
      let attribs = {};
      try {
        attribs = JSON.parse(str);
      } catch (e) {
        this.$utils.toast(
          `${this.$t('subscribers.invalidJSON')}: ${e.toString()}`,
          'is-danger',

          3000,
        );
        return null;
      }
      if (attribs instanceof Array) {
        this.$utils.toast('Attributes should be a map {} and not an array []', 'is-danger', 3000);
        return null;
      }

      return attribs;
    },
  },

  computed: {
    addrSuggestDomain() {
      return this.addrSuggest.split('@').pop();
    },

    ...mapState(['lists', 'loading']),

    // A blocklisted address is blocked for every team: only someone who reads all subscribers
    // takes it off the blocklist (the server checks the same).
    unblockLocked() {
      return this.isEditing && this.data.status === 'blocklisted' && !this.$can('subscribers:get_all');
    },

    // The subscriber is on a list the user may not manage (edit_locked from the server): the
    // profile is an administrator's to change, the user's own lists stay open.
    profileLocked() {
      return this.isEditing && !!this.data.editLocked;
    },

    statusHelp() {
      if (this.profileLocked) return this.$t('teams.lockedField');
      return this.unblockLocked ? this.$t('teams.unblockAdminOnly') : this.$t('subscribers.blocklistedHelp');
    },

    hasOptinList() {
      return this.form.lists.some((l) => l.optin === 'double');
    },
  },

  mounted() {
    if (this.$props.isEditing) {
      this.form = {
        ...this.$props.data,

        // Deep-copy the lists array on to the form.
        strAttribs: JSON.stringify(this.$props.data.attribs, null, 4),
      };
    }

    if (this.form.id) {
      this.getBounces();
    }

    this.$nextTick(() => {
      if (!this.profileLocked) {
        this.$refs.focus.focus();
      }
    });
  },
});
</script>

<style scoped>
.addr-suggest { margin: -0.5rem 0 1rem; }
.other-teams-box { background: #f8fafb; border: 1px solid #dce3e8; border-radius: 5px; padding: 10px 14px; margin: 0 0 16px; }
.other-teams-box .label { margin-bottom: 4px; }
.lock-icon { width: 1.05em; height: 1.05em; vertical-align: -0.15em; fill: none; stroke: currentColor; stroke-width: 1.8;
  stroke-linejoin: round; stroke-linecap: round; }
.locked-notice { padding: 12px 16px; margin-bottom: 20px; }
.locked-notice p { margin: 0 0 4px; }
.locked-notice .locked-title { font-weight: 600; }
.locked-notice .tags { margin-top: 8px; margin-bottom: 0; }
.locked-notice .tag { background: #fff; border: 1px solid #ecd9a0; color: #7a5b00; }
</style>
