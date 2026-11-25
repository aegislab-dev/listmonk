<template>
  <b-tooltip :label="text" multilined position="is-bottom" type="is-light" class="st-help" :data-cy="`help-${field}`">
    <i class="st-q" tabindex="0">?</i>
  </b-tooltip>
</template>

<script>
import { helpText } from '../stats-definitions';

// The "?" next to a figure: the same sentences as the folded table of definitions.
export default {
  name: 'StatsHelp',
  props: { field: { type: String, required: true } },
  computed: { text() { return helpText(this.field); } },
};
</script>
