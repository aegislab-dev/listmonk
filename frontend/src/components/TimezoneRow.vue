<template>
  <button type="button" class="tzp-row" :class="{ 'is-selected': selected }" :data-tz="entry.tz"
    :aria-pressed="selected ? 'true' : 'false'" @click="$emit('pick', entry.tz)">
    <span class="tzp-chip">{{ entry.abbr }}</span>
    <span class="tzp-name">
      <span>{{ entry.name }}</span><span v-if="entry.city" class="tzp-muted"> · {{ entry.city }}</span>
      <span class="tzp-iana">{{ entry.tz }}</span>
    </span>
    <span class="tzp-time">{{ entry.offset }}<br>{{ entry.parts.hm }}</span>
    <span class="tzp-check"><b-icon v-if="selected" icon="check-circle-outline" size="is-small" /></span>
  </button>
</template>

<script>
import Vue from 'vue';

// One row of the time zone list: short name, name, city, IANA name, UTC offset, and the time of the
// scheduled moment in that zone.
export default Vue.extend({
  name: 'TimezoneRow',
  props: {
    entry: { type: Object, required: true },
    selected: { type: Boolean, default: false },
  },
});
</script>

<style scoped>
.tzp-muted { color: #6b7280; }
.tzp-row { display: flex; align-items: center; gap: .6rem; width: 100%; padding: .38rem .75rem; border: 0; background: none; text-align: left; cursor: pointer; font: inherit; color: #111827; }
.tzp-row:hover, .tzp-row:focus { background: #f3f6fd; outline: none; }
.tzp-row.is-selected { background: #eef4ff; }
.tzp-chip { flex: none; min-width: 3.4rem; text-align: center; padding: 0 .35rem; border-radius: 4px; background: #eef0f3; color: #374151; font-size: .75rem; font-weight: 700; line-height: 1.5; }
.is-selected .tzp-chip { background: #dbe7ff; }
.tzp-name { flex: 1; min-width: 0; overflow-wrap: anywhere; }
.tzp-iana { display: block; font-size: .7rem; color: #9ca3af; }
.tzp-time { flex: none; font-size: .75rem; color: #6b7280; text-align: right; font-variant-numeric: tabular-nums; }
.tzp-check { flex: none; width: 1.2rem; color: #1d4ed8; }
</style>
