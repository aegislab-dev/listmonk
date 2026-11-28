<template>
  <b-tooltip :triggers="['hover', 'focus', 'click']" multilined :position="position" type="is-light"
    class="st-help" :style="{ '--st-help-shift': `${shift}px` }" :data-cy="`help-${field}`" @open="place" @click.native.stop>
    <template #content>
      <div v-if="parts" class="st-help-body">
        <div class="st-help-name">{{ parts.name }}</div>
        <div>{{ parts.meaning }}</div>
        <div class="st-help-rows">
          <template v-for="r in parts.rows">
            <span :key="`${r.key}-label`" class="st-help-label">{{ r.label }}</span>
            <span :key="`${r.key}-text`">{{ r.text }}</span>
          </template>
        </div>
        <div v-if="details.length" class="st-help-details">
          <div v-for="(d, i) in details" :key="i">{{ d }}</div>
        </div>
      </div>
    </template>
    <button class="st-q" type="button" :aria-label="$t('stats.defsLink')">?</button>
  </b-tooltip>
</template>

<script>
import { helpParts } from '../stats-definitions';

// Space kept between the bubble and the edge of the visible area. The width is in the style below.
const EDGE = 12;
const WIDTH = 340;

// The "?" next to a figure: the same sentences as the folded table of definitions, one item per line.
// The bubble opens above the "?" so that it does not cover the figures under it, moves sideways to
// stay inside the screen, and opens below only when there is no room above.
export default {
  name: 'StatsHelp',
  props: {
    field: { type: String, required: true },
    details: { type: Array, default: () => [] },
  },
  data() { return { position: 'is-top', shift: 0 }; },
  computed: {
    parts() { return helpParts(this.field, this.$t.bind(this)); },
  },
  methods: {
    place() {
      const at = this.$el.getBoundingClientRect();
      const screen = document.documentElement.clientWidth;
      const main = this.$el.closest('.main');
      const min = Math.max(0, main ? main.getBoundingClientRect().left : 0) + EDGE;
      const width = Math.min(WIDTH, screen - 2 * EDGE);
      const centred = at.left + at.width / 2 - width / 2;
      this.shift = Math.round(Math.max(Math.min(centred, screen - EDGE - width), Math.min(min, screen - EDGE - width)) - centred);
      this.position = 'is-top';
      this.$nextTick(() => {
        const bubble = this.$el.querySelector('.tooltip-content');
        const bar = document.querySelector('.navbar.is-fixed-top');
        const top = bar ? bar.getBoundingClientRect().bottom : 0;
        if (bubble && bubble.getBoundingClientRect().top < top + 4) this.position = 'is-bottom';
      });
    },
  },
};
</script>

<style scoped>
.st-q { padding: 0; }
.st-help.b-tooltip.is-multiline::v-deep .tooltip-content {
  width: min(340px, calc(100vw - 24px));
  padding: 10px 12px;
  color: #2b2f36;
  font-size: 12.5px;
  line-height: 1.5;
  text-align: left;
  white-space: normal;
  word-break: keep-all;
  overflow-wrap: break-word;
  transform: translateX(calc(-50% + var(--st-help-shift, 0px)));
}
.st-help.b-tooltip.is-multiline::v-deep .tooltip-content::before { left: calc(50% - var(--st-help-shift, 0px)); }
.st-help-name { font-size: 13px; font-weight: 700; }
.st-help-rows {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: 4px 10px;
  margin-top: 7px;
  padding-top: 7px;
  border-top: 1px solid #dde1e6;
}
.st-help-label { color: #6b7280; font-weight: 600; white-space: nowrap; }
.st-help-details { margin-top: 7px; padding-top: 7px; border-top: 1px solid #dde1e6; }
.st-help-details > div + div { margin-top: 2px; }
</style>
