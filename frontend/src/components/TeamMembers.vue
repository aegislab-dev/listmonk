<template>
  <form @submit.prevent="onSubmit">
    <div class="modal-card content" style="width: auto" data-cy="team-members">
      <header class="modal-card-head">
        <h4>{{ $t('teams.membersTitle', { name: team.name }) }}</h4>
      </header>
      <section class="modal-card-body">
        <p class="is-size-7 has-text-grey">{{ $t('teams.membersHelp') }}</p>
        <b-table :data="users" :loading="!loaded" narrowed>
          <b-table-column v-slot="props" width="40">
            <b-checkbox v-model="selected" :native-value="props.row.id" :disabled="!!blocked(props.row)"
              :data-cy="`member-${props.row.username}`" />
          </b-table-column>
          <b-table-column v-slot="props" field="name" :label="$t('globals.fields.name')">
            {{ props.row.name }}
            <div class="is-size-7 has-text-grey">{{ props.row.username }}</div>
          </b-table-column>
          <b-table-column v-slot="props" field="role" :label="$tc('users.role')">
            {{ props.row.userRole.name }}
          </b-table-column>
          <b-table-column v-slot="props" field="team" :label="$t('teams.currentTeam')">
            {{ currentTeam(props.row) }}
            <div v-if="warning(props.row)" class="member-warn">{{ warning(props.row) }}</div>
          </b-table-column>
        </b-table>
        <p class="notification is-warning is-light">{{ $t('teams.membersNote') }}</p>
      </section>
      <footer class="modal-card-foot has-text-right">
        <b-button @click="$parent.close()">{{ $t('globals.buttons.close') }}</b-button>
        <b-button native-type="submit" type="is-primary" :loading="saving" data-cy="btn-save">
          {{ $t('globals.buttons.save') }}
        </b-button>
      </footer>
    </div>
  </form>
</template>

<script>
import Vue from 'vue';

export default Vue.extend({
  name: 'TeamMembers',

  props: {
    team: { type: Object, required: true },
    teams: { type: Array, default: () => [] },
    userRoles: { type: Array, default: () => [] },
  },

  data() {
    return {
      users: [],
      selected: this.team.members.map((m) => m.id),
      loaded: false,
      saving: false,
    };
  },

  methods: {
    roleOf(user) { return (this.userRoles || []).find((r) => r.id === user.userRole.id); },

    readsAllLists(user) {
      const role = this.roleOf(user);
      return !!role && (role.permissions || []).some((p) => p === 'lists:get_all' || p === 'lists:manage_all');
    },

    // A super admin cannot join; someone who reads all lists needs the team's default role.
    blocked(user) {
      if (user.userRole.id === 1) return this.$t('teams.superAdmin');
      if (this.readsAllLists(user) && !this.team.userRoleId && !this.isMember(user)) return this.$t('teams.needsUserRole');
      return '';
    },

    isMember(user) { return this.team.members.some((m) => m.id === user.id); },

    currentTeam(user) {
      if (!user.listRole) return this.$t('teams.noTeam');
      return this.isMember(user) ? this.$t('teams.thisTeam') : user.listRole.name;
    },

    // What joining changes for this person: another team, or a user role.
    warning(user) {
      const blocked = this.blocked(user);
      if (blocked) return blocked;
      if (this.isMember(user)) return '';
      const out = [];
      if (user.listRole) out.push(this.$t('teams.moves'));
      if (this.team.userRoleId && user.userRole.id !== this.team.userRoleId) {
        out.push(this.readsAllLists(user) ? this.$t('teams.roleChangesAll') : this.$t('teams.roleChanges'));
      }
      return out.join(' · ');
    },

    onSubmit() {
      this.saving = true;
      this.$api.setTeamMembers(this.team.id, this.selected).then(() => {
        this.$emit('finished');
        this.$parent.close();
        this.$utils.toast(this.$t('globals.messages.updated', { name: this.team.name }));
      }).finally(() => { this.saving = false; });
    },
  },

  mounted() {
    this.$api.getUsers().then((users) => {
      this.users = (users || []).filter((u) => u.status === 'enabled' || this.isMember(u));
      this.loaded = true;
    });
  },
});
</script>

<style scoped>
.member-warn { color: #b36b00; font-size: .8rem; }
</style>
