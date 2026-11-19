<template>
  <div ref="root" class="tzp control" data-cy="send-tz">
    <button type="button" class="button tzp-button" :disabled="disabled" :title="$t('campaigns.sendTz.change')"
      aria-haspopup="dialog" :aria-expanded="open ? 'true' : 'false'" data-cy="send-tz-button" @click="toggle">
      <span class="tzp-current">
        <b>{{ current.abbr }}</b>
        <span class="tzp-muted">{{ current.name }}</span>
      </span>
      <span class="tzp-caret" aria-hidden="true" />
    </button>

    <div v-if="open" class="tzp-panel" role="dialog" :aria-label="$t('campaigns.sendTz.change')" data-cy="send-tz-panel" @keydown.esc.stop="close">
      <div class="tzp-search">
        <b-icon icon="magnify" size="is-small" />
        <input ref="search" v-model="query" type="text" class="tzp-input" autocomplete="off" spellcheck="false"
          :aria-label="$t('campaigns.sendTz.search')" :placeholder="$t('campaigns.sendTz.search')" data-cy="send-tz-search">
      </div>

      <div class="tzp-list">
        <template v-if="query.trim()">
          <p class="tzp-head">{{ $t('campaigns.sendTz.results', { count: results.length }) }}</p>
          <p v-if="!results.length" class="tzp-empty">{{ $t('campaigns.sendTz.none') }}</p>
          <zone-row v-for="e in shown" :key="e.tz" :entry="e" :selected="e.tz === value" @pick="pick" />
          <p v-if="results.length > shown.length" class="tzp-empty">… {{ results.length - shown.length }}</p>
        </template>
        <template v-else>
          <p class="tzp-head">{{ $t('campaigns.sendTz.frequent') }}</p>
          <zone-row v-for="e in frequent" :key="`f-${e.tz}`" :entry="e" :selected="e.tz === value" @pick="pick" />
          <template v-if="recent.length">
            <p class="tzp-head">{{ $t('campaigns.sendTz.recent') }}</p>
            <zone-row v-for="e in recent" :key="`r-${e.tz}`" :entry="e" :selected="e.tz === value" @pick="pick" />
          </template>
          <p class="tzp-head">{{ $t('campaigns.sendTz.all', { count: index.length }) }}</p>
          <zone-row v-for="e in index" :key="`a-${e.tz}`" :entry="e" :selected="e.tz === value" @pick="pick" />
        </template>
      </div>
    </div>
  </div>
</template>

<script>
import Vue from 'vue';
import {
  allZones, buildIndex, FREQUENT_ZONES, recentZones, rememberZone, searchIndex, zoneInfo,
} from '../schedule-time';
import TimezoneRow from './TimezoneRow.vue';

const MAX_RESULTS = 60;

export default Vue.extend({
  name: 'TimezonePicker',
  components: { ZoneRow: TimezoneRow },

  props: {
    // The chosen IANA zone.
    value: { type: String, default: 'Asia/Seoul' },
    // The scheduled moment (Date), or null: then the rows show the time now.
    instant: { type: Date, default: null },
    disabled: { type: Boolean, default: false },
  },

  data() {
    return {
      open: false,
      query: '',
      index: [],
      recent: [],
      frequent: [],
      at: new Date(),
    };
  },

  computed: {
    current() {
      return zoneInfo(this.value, this.instant || this.at);
    },

    results() {
      return searchIndex(this.query, this.index);
    },

    shown() {
      return this.results.slice(0, MAX_RESULTS);
    },
  },

  methods: {
    toggle() {
      if (this.open) this.close();
      else this.show();
    },

    show() {
      // Rows show the time of the scheduled moment (now when none is chosen yet).
      this.at = this.instant || new Date();
      const t = this.at;
      this.index = buildIndex(allZones(), t);
      const byTz = new Map(this.index.map((e) => [e.tz, e]));
      this.frequent = FREQUENT_ZONES.map((tz) => byTz.get(tz)).filter(Boolean);
      this.recent = recentZones().filter((tz) => !FREQUENT_ZONES.includes(tz)).map((tz) => byTz.get(tz)).filter(Boolean);
      this.query = '';
      this.open = true;
      document.addEventListener('mousedown', this.onOutside);
      this.$nextTick(() => { if (this.$refs.search) this.$refs.search.focus(); });
    },

    close() {
      this.open = false;
      document.removeEventListener('mousedown', this.onOutside);
    },

    onOutside(e) {
      if (this.$refs.root && !this.$refs.root.contains(e.target)) this.close();
    },

    pick(tz) {
      rememberZone(tz);
      this.close();
      this.$emit('input', tz);
    },
  },

  beforeDestroy() {
    document.removeEventListener('mousedown', this.onOutside);
  },
});
</script>

<style scoped>
.tzp { position: relative; }
.tzp-button { min-width: 12.5rem; justify-content: space-between; gap: .5rem; height: 100%; }
.tzp-muted { color: #6b7280; }
.tzp-current > b { margin-right: .4rem; }
@media (max-width: 520px) {
  .tzp-button { min-width: 0; }
  .tzp-current .tzp-muted { display: none; }
}
.tzp-caret { width: 0; height: 0; border-left: 4px solid transparent; border-right: 4px solid transparent; border-top: 5px solid #6b7280; flex: none; }
.tzp-panel { position: absolute; right: 0; top: calc(100% + 4px); z-index: 40; width: 440px; max-width: 92vw; background: #fff; border: 1px solid #dfe3e8; border-radius: 8px; box-shadow: 0 8px 24px rgba(0, 0, 0, .14); font-size: .8125rem; overflow: hidden; }
.tzp-search { display: flex; align-items: center; gap: .4rem; padding: .5rem .6rem; border-bottom: 1px solid #eef0f3; color: #9ca3af; }
.tzp-input { flex: 1; border: 1px solid #dbdbdb; border-radius: 4px; padding: .3rem .55rem; font-size: .8125rem; }
.tzp-input:focus { outline: none; border-color: #3273dc; }
.tzp-list { max-height: 340px; overflow-y: auto; padding-bottom: .3rem; }
.tzp-head { padding: .45rem .75rem .15rem; font-size: .7rem; font-weight: 700; color: #6b7280; letter-spacing: .02em; }
.tzp-empty { padding: .4rem .75rem; color: #6b7280; }
</style>
