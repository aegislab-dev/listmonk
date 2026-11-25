<template>
  <div class="st-fold-wrap" data-cy="stats-definitions">
    <div class="st-fold" :class="{ open: value }" data-cy="defs-toggle" tabindex="0" role="button"
      @click="$emit('input', !value)" @keyup.enter="$emit('input', !value)">
      {{ $t('stats.defsToggle') }} · <span class="st-note">{{ $t('stats.defsNote') }}</span> <i class="st-tri" :class="value ? 'up' : 'down'" />
    </div>
    <div v-if="value" class="st-card" style="border-top:0;border-radius:0 0 8px 8px;margin-top:-17px" data-cy="defs-table">
      <h2>{{ $t('stats.defs') }}<small>{{ $t('stats.defsRowNote') }}</small></h2>
      <table class="st-table">
        <thead><tr><th>{{ $t('stats.defName') }}</th><th style="text-align:left">{{ $t('stats.defMeaning') }}</th><th style="text-align:left">{{ $t('stats.defCompare') }}</th></tr></thead>
        <tbody>
          <template v-for="d in defs">
            <tr :key="d.key" style="cursor:pointer" :data-cy="`def-${d.key}`" @click="toggle(d.key)">
              <td style="white-space:nowrap"><b><i class="st-tri" :class="open[d.key] ? 'down' : 'right'" /> {{ d.name }}</b></td>
              <td style="text-align:left">
{{ d.meaning }}
                <div v-if="open[d.key]" class="st-note" style="margin:4px 0 2px 18px;padding:7px 10px;background:#f7f8fa;border-radius:5px">
                  {{ $t('stats.formula') }} · {{ d.formula }}<br />{{ $t('stats.minSample') }} · {{ d.sample }}
                </div>
              </td>
              <td style="text-align:left">{{ d.compare }}</td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script>
import { DEFINITIONS } from '../stats-definitions';

export default {
  name: 'StatsDefinitions',
  props: { value: { type: Boolean, default: false } },
  data() { return { defs: DEFINITIONS, open: {} }; },
  methods: { toggle(k) { this.open = { ...this.open, [k]: !this.open[k] }; } },
};
</script>
