<template>
  <div>
    <b-modal scroll="keep" @close="close" :aria-modal="true" :active="isVisible">
      <div>
        <div class="modal-card" style="width: auto">
          <header class="modal-card-head">
            <h4>
              {{ title }}
              <span v-if="sampleLabel" class="sample-note" data-cy="preview-sample">
                <b-icon icon="account-outline" size="is-small" /> {{ sampleLabel }}
              </span>
            </h4>
          </header>
        </div>
        <section expanded class="modal-card-body preview">
          <b-loading :active="isLoading" :is-full-page="false" />
          <form v-if="isPost" method="post" :action="previewURL" target="iframe" ref="form">
            <input v-if="templateId" type="hidden" name="template_id" :value="templateId" />
            <input v-if="contentType" type="hidden" name="content_type" :value="contentType" />
            <input v-if="templateType" type="hidden" name="template_type" :value="templateType" />
            <input v-if="archiveMeta" type="hidden" name="archive_meta" :value="archiveMeta" />
            <input v-if="body" type="hidden" name="body" :value="body" />
            <input v-if="sample" type="hidden" name="sample" :value="sample" />
          </form>

          <iframe id="iframe" name="iframe" ref="iframe" :title="title" :src="isPost ? 'about:blank' : previewURL"
            @load="onLoaded" sandbox="allow-scripts" />
        </section>
        <footer class="modal-card-foot has-text-right">
          <b-button @click="close">
            {{ $t('globals.buttons.close') }}
          </b-button>
        </footer>
      </div>
    </b-modal>
  </div>
</template>

<script>
import { uris } from '../constants';

export default {
  name: 'CampaignPreview',

  props: {
    isPost: { type: Boolean, default: false },

    // Template or campaign ID.
    id: { type: Number, default: 0 },
    title: { type: String, default: '' },

    // campaign | template.
    type: { type: String, default: '' },

    // campaign | tx.
    templateType: { type: String, default: '' },

    archiveMeta: { type: String, default: null },

    body: { type: String, default: '' },
    contentType: { type: String, default: '' },
    templateId: { type: [Number, null], default: null },
    isArchive: { type: Boolean, default: false },

    // AXM (LST-41): the sample of the campaign test box, as JSON, and its name for the header.
    sample: { type: String, default: '' },
    sampleLabel: { type: String, default: '' },
  },

  data() {
    return {
      isVisible: true,
      isLoading: true,
      formSubmitted: false,
    };
  },

  methods: {
    close() {
      this.$emit('close');
      this.isVisible = false;
    },

    // On iframe load, kill the spinner.
    onLoaded() {
      if (!this.isPost) {
        this.isLoading = false;
        return;
      }

      if (this.formSubmitted) {
        this.isLoading = false;
      }
    },
  },

  computed: {
    previewURL() {
      let uri = 'about:blank';

      if (this.type === 'campaign') {
        uri = this.isArchive ? uris.previewCampaignArchive : uris.previewCampaign;
      } else if (this.type === 'template') {
        if (this.id) {
          uri = uris.previewTemplate;
        } else {
          uri = uris.previewRawTemplate;
        }
      }

      return uri.replace(':id', this.id);
    },
  },

  mounted() {
    if (this.isPost) {
      setTimeout(() => {
        this.$refs.form.submit();
        this.formSubmitted = true;
      }, 100);
    }
  },
};
</script>

<style scoped>
.sample-note {
  display: inline-flex; align-items: center; gap: 0.3rem; font-size: 0.8rem; background: #eef4ff; color: #0b3d91;
  border-radius: 4px; padding: 0.15rem 0.6rem; margin-left: 0.6rem; vertical-align: middle; overflow-wrap: anywhere;
}
</style>
