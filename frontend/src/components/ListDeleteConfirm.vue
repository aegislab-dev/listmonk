<template>
  <form @submit.prevent="onSubmit">
    <div class="modal-card content" style="width: auto" data-cy="list-delete-confirm">
      <header class="modal-card-head">
        <h4 class="has-text-danger">
          {{ single ? $t('lists.delete.title', { name: lists[0].name }) : $t('lists.delete.titleMany', { num: total }) }}
        </h4>
      </header>
      <section class="modal-card-body">
        <div v-if="foreign.length" class="notification other-team-warn" data-cy="other-team-warn">
          <b-icon icon="warning-empty" size="is-small" />
          <strong v-if="single">{{ $t('lists.delete.otherTeam', { teams: foreignTeamNames }) }}</strong>
          <strong v-else>{{ $t('lists.delete.otherTeamMany', { num: foreign.length, teams: foreignTeamNames }) }}</strong>
        </div>

        <p data-cy="delete-trash-note">{{ $t('lists.delete.trashNote', { days: trashDays }) }}</p>

        <ul v-if="!single && lists.length" class="delete-lists" data-cy="delete-list-names">
          <li v-for="l in shown" :key="l.id">
            <strong>{{ l.name }}</strong>
            <b-tag v-for="t in otherTags(l)" :key="t" type="is-danger is-light" class="ml-2" data-cy="other-team-tag">
              {{ teamName(t) }}
            </b-tag>
            <span class="has-text-grey"> · {{ $t('lists.delete.numSubs', { num: l.subscriberCount }) }}</span>
          </li>
          <li v-if="more > 0" class="has-text-grey">{{ $t('lists.delete.more', { num: more }) }}</li>
        </ul>
        <p v-if="single" class="has-text-grey" data-cy="delete-subs">
          {{ $t('lists.delete.numSubs', { num: lists[0].subscriberCount }) }}
        </p>
        <p v-else-if="lists.length" class="has-text-grey" data-cy="delete-subs">
          {{ $t('lists.delete.totalSubs', { num: totalSubs }) }}
        </p>

        <b-field :label="single ? $t('lists.delete.typeName', { name: lists[0].name }) : $t('lists.delete.typeCount', { num: total })"
          label-position="on-border" class="mt-5">
          <b-input v-model="typed" ref="focus" autocomplete="off" data-cy="delete-confirm-input"
            :placeholder="single ? lists[0].name : String(total)" />
        </b-field>
      </section>
      <footer class="modal-card-foot has-text-right">
        <b-button @click="$parent.close()" data-cy="btn-delete-cancel">{{ $t('globals.buttons.cancel') }}</b-button>
        <b-button native-type="submit" type="is-danger" :disabled="!matches" data-cy="btn-delete-confirm">
          {{ $t('globals.buttons.delete') }}
        </b-button>
      </footer>
    </div>
  </form>
</template>

<script>
import Vue from 'vue';
import { otherTeamTags, teamName } from '../teams';

// Asks for the name of the list (or, for several lists, their number) before they go to the trash.
// The server does not ask: this guards against a slip of the hand, the trash against the rest.
export default Vue.extend({
  name: 'ListDeleteConfirm',

  props: {
    // The lists that go: { id, name, subscriberCount }. For a delete of every list a search
    // finds, the lists are not known: pass an empty array and total.
    lists: { type: Array, default: () => [] },
    total: { type: Number, default: 0 },
    trashDays: { type: Number, default: 14 },
  },

  data() {
    return { typed: '' };
  },

  computed: {
    // The lists that belong to a team other than the person's own (the server allows the delete
    // when the person may manage the list; this makes the person see it).
    foreign() { return this.lists.filter((l) => this.otherTags(l).length > 0); },

    foreignTeamNames() {
      const names = [];
      this.foreign.forEach((l) => this.otherTags(l).forEach((t) => {
        if (!names.includes(teamName(t))) names.push(teamName(t));
      }));
      return names.join(', ');
    },

    single() { return this.lists.length === 1 && this.total === 1; },

    shown() { return this.lists.slice(0, 8); },

    more() { return this.total - this.shown.length; },

    totalSubs() { return this.lists.reduce((n, l) => n + (l.subscriberCount || 0), 0); },

    matches() {
      const v = this.typed.trim();
      return this.single ? v === this.lists[0].name.trim() : v === String(this.total);
    },
  },

  methods: {
    teamName,

    otherTags(l) { return otherTeamTags(l.tags, this.$teamTag()); },

    onSubmit() {
      if (!this.matches) return;
      this.$emit('confirm');
      this.$parent.close();
    },
  },

  mounted() {
    this.$nextTick(() => { if (this.$refs.focus) this.$refs.focus.focus(); });
  },
});
</script>

<style scoped>
.delete-lists {
  margin-top: 0.75rem;
}
</style>
