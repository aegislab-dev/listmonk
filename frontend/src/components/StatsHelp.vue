<template>
  <b-tooltip :label="text" :triggers="['hover', 'focus', 'click']" multilined position="is-bottom" type="is-light"
    class="st-help" :data-cy="`help-${field}`" @click.native.stop>
    <button class="st-q" type="button" :aria-label="$t('stats.defsLink')">?</button>
  </b-tooltip>
</template>

<script>
import { helpText } from '../stats-definitions';

// The "?" next to a figure: the same sentences as the folded table of definitions.
export default {
  name: 'StatsHelp',
  props: {
    field: { type: String, required: true },
    details: { type: Array, default: () => [] },
  },
  computed: {
    text() { return [helpText(this.field, this.$t.bind(this)), ...this.details].join('\n'); },
  },
};
</script>

<style scoped>
.st-q { padding: 0; }
.st-help::v-deep .tooltip-content { white-space: pre-line; }
</style>
