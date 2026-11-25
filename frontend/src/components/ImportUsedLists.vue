<template>
  <b-message v-if="entries.length" type="is-warning" :closable="false" class="has-text-left used-lists"
    data-cy="import-used-list">
    <div v-for="e in entries" :key="e.list.id" class="used-entry" :data-cy="`import-used-${e.list.id}`">
      <strong>{{ $t('import.used.title', { list: e.list.name }) }}</strong>
      <table class="used-table">
        <tr v-for="u in e.items" :key="u.campaignId" :data-cy="`import-used-camp-${u.campaignId}`">
          <td>
            <router-link v-if="u.name && canRead" :to="{ name: 'campaign', params: { id: u.campaignId } }">
              #{{ u.campaignId }} 「{{ u.name }}」
            </router-link>
            <span v-else-if="u.name">#{{ u.campaignId }} 「{{ u.name }}」</span>
            <span v-else>{{ $t('import.used.noName', { id: u.campaignId }) }}</span>
          </td>
          <td>{{ line(u) }}</td>
        </tr>
        <tr v-if="e.total > e.items.length">
          <td colspan="2">{{ $t('import.used.more', { count: e.total - e.items.length }) }}</td>
        </tr>
      </table>
    </div>
    <p class="used-help">
      <span v-if="merge">{{ $t('import.used.helpMerge') }}</span>
      {{ $t('import.used.helpSend') }}
      <strong>{{ $t('import.used.helpStrong') }}</strong>
      {{ $t('import.used.helpEnd') }}
      <a v-if="newList" href="#" data-cy="import-used-new-list" @click.prevent="$emit('new-list')">
        {{ $t('import.used.newList') }}
      </a>
    </p>
  </b-message>
</template>

<script>
// What a list was used for, as a warning that never blocks (LST-63): "this list already went
// out in campaign #N to M people". Shown under the list field of the import and again on the
// finished screen, in the shape of the import result boxes.
export default {
  name: 'ImportUsedLists',

  props: {
    // [{ list: { id, name }, items: [campaign use], total }]
    entries: { type: Array, default: () => [] },
    // The import screen says what an import into the list does; the finished screen has done it.
    merge: { type: Boolean, default: false },
    // Offers "make a new list" (only where the screen can take it).
    newList: { type: Boolean, default: false },
  },

  computed: {
    // The name of a campaign comes only to a user who may read campaigns; the link needs it too.
    canRead() { return this.$can('campaigns:get_all', 'campaigns:get'); },
  },

  methods: {
    line(u) {
      const count = this.$utils.formatNumber(u.sent);
      if (u.status === 'scheduled') {
        return this.$t('import.used.scheduled', { when: u.sendAt ? this.$utils.niceDate(u.sendAt, true) : '' }) + this.many(u);
      }
      const key = ['cancelled', 'running', 'paused'].includes(u.status) ? u.status : 'sent';
      const date = this.$utils.getDate(u.startedAt || u.sendAt || u.updatedAt).format('YYYY-MM-DD');
      return `${date} · ${this.$t(`import.used.${key}`, { count })}${this.many(u)}`;
    },

    // The count is the whole campaign's: with several lists it is not this list's share.
    many(u) {
      return u.listsN > 1 ? ` ${this.$t('import.used.manyLists', { n: u.listsN })}` : '';
    },
  },
};
</script>

<style scoped>
.used-lists { margin-top: 0.75rem; }
.used-entry + .used-entry { margin-top: 0.75rem; }
.used-table { width: 100%; margin: 0.4rem 0; border-collapse: collapse; }
.used-table td { padding: 0.15rem 0.25rem; border-top: 1px solid rgba(97, 70, 0, 0.2); }
.used-help { margin-top: 0.4rem; }
</style>
