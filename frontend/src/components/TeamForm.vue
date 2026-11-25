<template>
  <form @submit.prevent="onSubmit">
    <div class="modal-card content" style="width: auto" data-cy="team-form">
      <header class="modal-card-head">
        <h4>{{ isEditing ? $t('teams.editTitle', { name: data.name }) : $t('teams.newTitle') }}</h4>
      </header>
      <section class="modal-card-body">
        <b-field :label="$t('globals.fields.name')" label-position="on-border" :type="error ? 'is-danger' : ''"
          :message="error || $t('teams.nameHelp', { tag: newTag })">
          <b-input v-model="form.name" :maxlength="90" ref="focus" required data-cy="team-name-input"
            :placeholder="$t('globals.fields.name')" />
        </b-field>
        <b-field :label="$t('teams.defaultUserRole')" label-position="on-border" :message="$t('teams.defaultUserRoleHelp')">
          <b-select v-model="form.userRoleId" expanded data-cy="team-user-role">
            <option :value="0">{{ $t('teams.noDefaultRole') }}</option>
            <option v-for="r in choices" :key="r.id" :value="r.id">
              {{ r.name }}{{ isWide(r) ? ` ${$t('teams.wideRoleOption')}` : '' }}
            </option>
          </b-select>
        </b-field>
        <p v-if="selectedWide.length" class="notification is-danger is-light" data-cy="team-wide-blocked">
          {{ $t('teams.wideRoleBlocked', { perms: selectedWide.join(', ') }) }}
          <router-link :to="{ path: '/users/roles/users' }" @click.native="$parent.close()">
            {{ $t('teams.wideFix') }}
          </router-link>
        </p>
        <b-checkbox v-if="isEditing && data.members.length && form.userRoleId" v-model="form.applyToMembers"
          data-cy="team-apply">
          {{ $t('teams.applyToMembers', { num: data.members.length }) }}
        </b-checkbox>
        <p v-if="!isEditing" class="notification is-info is-light" data-cy="team-new-note">
          {{ $t('teams.newNote', { tag: newTag }) }}
        </p>
        <p v-if="renames" class="notification is-warning is-light" data-cy="team-rename-warn">
          {{ $t('teams.renameWarn', { old: data.tag, tag: newTag, num: data.lists }) }}
        </p>
      </section>
      <footer class="modal-card-foot has-text-right">
        <b-button @click="$parent.close()">{{ $t('globals.buttons.close') }}</b-button>
        <b-button native-type="submit" type="is-primary" :loading="saving" :disabled="selectedWide.length > 0"
          data-cy="btn-save">
          {{ isEditing ? $t('globals.buttons.save') : $t('teams.create') }}
        </b-button>
      </footer>
    </div>
  </form>
</template>

<script>
import Vue from 'vue';
import { teamTagOf, widePermsOf } from '../teams';

export default Vue.extend({
  name: 'TeamForm',

  props: {
    data: { type: Object, default: null },
    userRoles: { type: Array, default: () => [] },
  },

  data() {
    return {
      form: {
        name: this.data ? this.data.name : '',
        userRoleId: this.data && this.data.userRoleId ? this.data.userRoleId : 0,
        applyToMembers: false,
      },
      error: '',
      saving: false,
    };
  },

  computed: {
    isEditing() { return !!this.data; },

    newTag() { return teamTagOf(this.form.name || '…'); },

    renames() { return this.isEditing && this.data.lists > 0 && this.newTag !== this.data.tag; },

    // A default role must not have a team-wide permission (LST-68): such a role would show
    // other teams' lists, subscribers or campaigns. A team whose role already has one keeps
    // that role in the list, marked, so that it does not look like "none".
    choices() {
      return (this.userRoles || []).filter((r) => r.id !== 1
        && (!this.isWide(r) || r.id === (this.data && this.data.userRoleId)));
    },

    selectedWide() {
      return widePermsOf((this.userRoles || []).find((r) => r.id === this.form.userRoleId));
    },
  },

  methods: {
    isWide(role) { return widePermsOf(role).length > 0; },

    onSubmit() {
      this.error = '';
      this.saving = true;
      const body = {
        name: this.form.name.trim(), user_role_id: this.form.userRoleId || 0, apply_to_members: this.form.applyToMembers,
      };
      const req = this.isEditing ? this.$api.updateTeam({ id: this.data.id, ...body }) : this.$api.createTeam(body);
      req.then((team) => {
        this.$emit('finished');
        this.$parent.close();
        this.$utils.toast(this.$t(this.isEditing ? 'globals.messages.updated' : 'globals.messages.created', { name: team.name }));
      }).finally(() => { this.saving = false; });
    },
  },

  mounted() {
    this.$nextTick(() => { if (this.$refs.focus) this.$refs.focus.focus(); });
  },
});
</script>
