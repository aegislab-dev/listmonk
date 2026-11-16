<template>
  <section class="teams">
    <header class="columns page-header">
      <div class="column is-10">
        <h1 class="title is-4 mb-2">
          {{ $t('teams.title') }}
          <span v-if="!loading.listRoles">({{ teams.length }})</span>
        </h1>
        <p class="is-size-7 has-text-grey" data-cy="teams-help">{{ $t('teams.help') }}</p>
      </div>
      <div class="column has-text-right">
        <b-field v-if="$can('roles:manage')" expanded>
          <b-button expanded type="is-primary" icon-left="plus" class="btn-new" data-cy="btn-new" @click="showNewForm">
            {{ $t('globals.buttons.new') }}
          </b-button>
        </b-field>
      </div>
    </header>

    <b-table :data="teams" :loading="loading.listRoles" hoverable>
      <b-table-column v-slot="props" field="name" :label="$t('globals.fields.name')">
        <a href="#" data-cy="team-name" @click.prevent="showEditForm(props.row)">{{ props.row.name }}</a>
        <div><b-tag class="is-small team-tag">{{ props.row.tag }}</b-tag></div>
      </b-table-column>

      <b-table-column v-slot="props" field="lists" :label="$t('globals.terms.lists')">
        <router-link :to="{ name: 'lists', query: { team: props.row.tag } }" data-cy="team-lists">
          {{ $t('teams.numLists', { num: props.row.lists }) }}
        </router-link>
      </b-table-column>

      <b-table-column v-slot="props" field="members" :label="$t('teams.members')">
        <template v-if="props.row.members.length">
          <b-taglist>
            <b-tag v-for="m in props.row.members" :key="m.id" data-cy="team-member">{{ m.name }}</b-tag>
          </b-taglist>
          <span class="is-size-7 has-text-grey">{{ $t('teams.numMembers', { num: props.row.members.length }) }}</span>
        </template>
        <span v-else class="has-text-grey">{{ $t('teams.noMembers') }}</span>
      </b-table-column>

      <b-table-column v-slot="props" field="userRole" :label="$t('teams.defaultUserRole')" cell-class="team-role-col"
        header-class="team-role-col">
        {{ userRoleName(props.row.userRoleId) }}
      </b-table-column>

      <b-table-column v-slot="props" cell-class="actions" align="right">
        <div v-if="$can('roles:manage')">
          <a v-if="$can('users:get')" href="#" data-cy="btn-members" :aria-label="$t('teams.manageMembers')"
            @click.prevent="showMembers(props.row)">
            <b-tooltip :label="$t('teams.manageMembers')" type="is-dark">
              <b-icon icon="account-multiple" size="is-small" />
            </b-tooltip>
          </a>
          <a href="#" data-cy="btn-edit" :aria-label="$t('globals.buttons.edit')" @click.prevent="showEditForm(props.row)">
            <b-tooltip :label="$t('globals.buttons.edit')" type="is-dark">
              <b-icon icon="pencil-outline" size="is-small" />
            </b-tooltip>
          </a>
          <a href="#" data-cy="btn-delete" :class="{ 'is-disabled': !isEmpty(props.row) }"
            :aria-label="$t('globals.buttons.delete')" @click.prevent="onDelete(props.row)">
            <b-tooltip :label="isEmpty(props.row) ? $t('globals.buttons.delete') : $t('teams.cantDelete')" type="is-dark">
              <b-icon icon="trash-can-outline" size="is-small" />
            </b-tooltip>
          </a>
        </div>
      </b-table-column>

      <template #empty v-if="!loading.listRoles">
        <empty-placeholder />
      </template>
    </b-table>

    <b-modal scroll="keep" :aria-modal="true" :active.sync="isFormVisible" :width="620">
      <team-form :data="curTeam" :user-roles="userRoles" @finished="load" />
    </b-modal>
    <b-modal scroll="keep" :aria-modal="true" :active.sync="isMembersVisible" :width="720">
      <team-members :team="curTeam" :teams="teams" :user-roles="userRoles" @finished="load" />
    </b-modal>
  </section>
</template>

<script>
import Vue from 'vue';
import { mapState } from 'vuex';
import EmptyPlaceholder from '../components/EmptyPlaceholder.vue';
import TeamForm from '../components/TeamForm.vue';
import TeamMembers from '../components/TeamMembers.vue';

export default Vue.extend({
  components: { EmptyPlaceholder, TeamForm, TeamMembers },

  data() {
    return {
      teams: [],
      curTeam: null,
      isFormVisible: false,
      isMembersVisible: false,
    };
  },

  computed: {
    ...mapState(['loading', 'userRoles']),
  },

  methods: {
    load() {
      this.$api.getTeams().then((teams) => { this.teams = teams; });
      this.$api.getUserRoles();
    },

    isEmpty(team) {
      return !team.members.length && !team.lists;
    },

    userRoleName(id) {
      const role = (this.userRoles || []).find((r) => r.id === id);
      return role ? role.name : '—';
    },

    showNewForm() {
      this.curTeam = null;
      this.isFormVisible = true;
    },

    showEditForm(team) {
      if (!this.$can('roles:manage')) return;
      this.curTeam = team;
      this.isFormVisible = true;
    },

    showMembers(team) {
      this.curTeam = team;
      this.isMembersVisible = true;
    },

    onDelete(team) {
      if (!this.isEmpty(team)) return;
      this.$utils.confirm(this.$t('teams.confirmDelete', { name: team.name }), () => {
        this.$api.deleteTeam(team.id).then(() => {
          this.$utils.toast(this.$t('globals.messages.deleted', { name: team.name }));
          this.load();
        });
      });
    },
  },

  created() {
    this.$root.$on('page.refresh', this.load);
  },

  destroyed() {
    this.$root.$off('page.refresh', this.load);
  },

  mounted() {
    this.load();
  },
});
</script>

<style scoped>
.team-tag { margin-top: 4px; }
.is-disabled { opacity: .35; cursor: not-allowed; }
@media screen and (max-width: 768px) {
  .team-role-col { display: none; }
}
</style>
