"""axm 의 선택 로직 단위시험. `python3 -m unittest scripts/axm/test_axm.py` (저장소 루트에서).

Docker 없이 돈다. 바뀐 파일 → spec·Go 시험 매핑, 매핑 표가 CI 와 어긋나지 않는지,
runtime.Dockerfile 이 Dockerfile.axm 마지막 단계와 같은지를 본다.
"""
import importlib.machinery
import importlib.util
import json
import re
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]

_loader = importlib.machinery.SourceFileLoader('axm_tool', str(HERE / 'axm'))
_spec = importlib.util.spec_from_loader('axm_tool', _loader)
axm = importlib.util.module_from_spec(_spec)
_loader.exec_module(axm)

FILES = axm.tree_files()
TMAP = axm.load_map()


def pick(*changed):
    return axm.affected(list(changed), FILES, TMAP)


class GlobTest(unittest.TestCase):
    def test_star_does_not_cross_directories(self):
        self.assertTrue(axm.match_any('cmd/axm_audit.go', ['cmd/axm_*.go']))
        self.assertFalse(axm.match_any('cmd/sub/axm_audit.go', ['cmd/axm_*.go']))

    def test_double_star(self):
        self.assertTrue(axm.match_any('internal/auth/a/b.go', ['internal/auth/**']))
        self.assertTrue(axm.match_any('docs/a/b.md', ['docs/**']))
        self.assertTrue(axm.match_any('README.md', ['*.md']))
        self.assertFalse(axm.match_any('cmd/handlers.go', ['internal/**']))


class SelectSpecTest(unittest.TestCase):
    def test_feature_file_selects_its_spec(self):
        r = pick('frontend/src/views/Audit.vue')
        self.assertEqual(r['specs'], ['audit.cy.js'])
        self.assertTrue(r['unit'])

    def test_backend_feature_file(self):
        r = pick('cmd/axm_audit.go')
        self.assertEqual(r['specs'], ['audit.cy.js'])

    def test_changed_spec_runs_itself(self):
        r = pick('frontend/cypress/e2e/list-draw.cy.js')
        self.assertEqual(r['specs'], ['list-draw.cy.js'])

    def test_upstream_spec_not_in_ci_is_skipped(self):
        r = pick('frontend/cypress/e2e/campaigns.cy.js')
        self.assertEqual(r['specs'], [])
        self.assertTrue(any('CI 가 돌리지 않는' in n for n in r['notes']))

    def test_shared_file_adds_smoke(self):
        r = pick('frontend/src/utils.js')
        self.assertEqual(set(r['specs']), set(TMAP['smoke']))
        self.assertTrue(any('전체 e2e' in n for n in r['notes']))

    def test_shared_plus_feature_is_union(self):
        r = pick('frontend/src/api/index.js', 'frontend/src/components/ListDraw.vue')
        self.assertEqual(set(r['specs']), set(TMAP['smoke']) | {'list-draw.cy.js'})

    def test_specs_come_in_ci_order(self):
        r = pick('frontend/src/api/index.js', 'frontend/src/components/AddressCheck.vue')
        order = TMAP['ci_specs']
        self.assertEqual(r['specs'], sorted(r['specs'], key=order.index))

    def test_unmapped_source_warns_and_adds_smoke(self):
        r = pick('frontend/src/views/Dashboard.vue')
        self.assertEqual(set(r['specs']), set(TMAP['smoke']))
        self.assertTrue(r['unmapped'])
        self.assertTrue(any('매핑 없음' in n for n in r['notes']))

    def test_docs_only_runs_nothing(self):
        r = pick('README.md', 'docs/docs/content/apis/lists.md')
        self.assertEqual((r['specs'], r['go'], r['unit']), ([], {}, False))
        self.assertTrue(r['docs_only'])

    def test_full_build_files(self):
        r = pick('Dockerfile.axm')
        self.assertTrue(r['full_build'])
        self.assertEqual(set(r['specs']), set(TMAP['smoke']))
        r = pick('frontend/yarn.lock')
        self.assertTrue(r['full_build'])

    def test_workflow_file_runs_nothing_locally(self):
        r = pick('.github/workflows/axm-image.yml')
        self.assertEqual((r['specs'], r['go'], r['full_build']), ([], {}, False))

    def test_tool_files_run_python_tests(self):
        r = pick('scripts/axm/axm')
        self.assertTrue(r['python'])
        self.assertFalse(r['docs_only'])


class SelectGoTest(unittest.TestCase):
    def test_feature_file_narrows_cmd_tests(self):
        r = pick('cmd/axm_audit.go')
        run = r['go']['cmd']
        self.assertIsNotNone(run)
        self.assertIn('TestAudit', run) if 'TestAudit' in run else self.assertTrue(run.startswith('^('))
        # 좁힌 목록은 패키지 전체 시험보다 훨씬 적다.
        allf = {fn for f in FILES if f.startswith('cmd/') and f.endswith('_test.go') for fn in axm.test_funcs(f)}
        self.assertLess(len(run.split('|')), len(allf))

    def test_shared_go_file_runs_whole_package(self):
        r = pick('cmd/handlers.go')
        self.assertIsNone(r['go']['cmd'])

    def test_mixed_feature_and_shared_runs_whole_package(self):
        r = pick('cmd/axm_audit.go', 'cmd/campaigns.go')
        self.assertIsNone(r['go']['cmd'])

    def test_queries_run_cmd_and_core(self):
        r = pick('queries/axm_audit.sql') if (ROOT / 'queries/axm_audit.sql').exists() else pick('schema.sql')
        self.assertIn('cmd', r['go'])
        self.assertIn('internal/core', r['go'])
        self.assertIsNone(r['go']['cmd'])

    def test_importers_of_a_changed_package_run(self):
        r = pick('internal/addrcheck/' + sorted(p.name for p in (ROOT / 'internal/addrcheck').glob('*.go') if not p.name.endswith('_test.go'))[0])
        self.assertIn('internal/addrcheck', r['go'])
        self.assertIn('cmd', r['go'])  # cmd 가 addrcheck 를 import 한다
        self.assertIsNone(r['go']['cmd'])

    def test_no_go_change_no_go_test(self):
        self.assertEqual(pick('frontend/src/views/Audit.vue')['go'], {})

    def test_package_without_tests_is_skipped(self):
        pk, _ = axm.go_packages(FILES)
        without = [d for d, fs in pk.items() if not any(f.endswith('_test.go') for f in fs)]
        if without:
            r = pick(pk[without[0]][0])
            self.assertNotIn(without[0], r['go'])


class MapIntegrityTest(unittest.TestCase):
    def test_ci_specs_match_workflow(self):
        wf = (ROOT / axm.CI_WORKFLOW).read_text()
        m = re.search(r"--spec '([^']+)'", wf)
        self.assertIsNotNone(m, '워크플로에서 --spec 줄을 못 찾았다')
        ci = [s.split('/')[-1] for s in m.group(1).split(',')]
        self.assertEqual(ci, TMAP['ci_specs'], 'test-map.json 의 ci_specs 가 axm-image.yml 의 --spec 과 다르다')

    def test_all_spec_files_exist(self):
        for s in TMAP['ci_specs']:
            self.assertTrue((ROOT / 'frontend/cypress/e2e' / s).exists(), s)

    def test_rules_point_to_ci_specs(self):
        for rule in TMAP['rules']:
            for s in rule['specs']:
                self.assertIn(s, TMAP['ci_specs'])
        for s in TMAP['smoke']:
            self.assertIn(s, TMAP['ci_specs'])

    def test_every_ci_spec_is_reachable_from_a_rule(self):
        reach = {s for rule in TMAP['rules'] for s in rule['specs']}
        self.assertEqual(reach, set(TMAP['ci_specs']))

    def test_every_rule_path_matches_an_existing_file(self):
        for rule in TMAP['rules']:
            for p in rule['paths']:
                self.assertTrue(any(axm.match_any(f, [p]) for f in FILES), f'규칙 경로가 어떤 파일에도 안 맞는다: {p}')

    def test_every_axm_cmd_file_has_a_rule_or_is_a_test(self):
        """cmd/axm_*.go 가 새로 생겼는데 규칙이 없으면 알려 준다."""
        allpaths = [p for r in TMAP['rules'] for p in r['paths']] + TMAP['shared']
        missing = [f for f in FILES if re.match(r'cmd/axm_[^/]*\.go$', f) and not f.endswith('_test.go')
                   and not axm.match_any(f, allpaths)]
        # 매핑이 없는 파일은 smoke + 경고로 처리되므로 실패가 아니라 목록만 확인한다.
        for f in missing:
            r = pick(f)
            self.assertTrue(r['unmapped'])


class RuntimeDockerfileTest(unittest.TestCase):
    def test_runtime_stage_equals_dockerfile_axm(self):
        axm_df = (ROOT / 'Dockerfile.axm').read_text()
        final = axm_df[axm_df.rindex('FROM alpine'):]
        mine = (HERE / 'runtime.Dockerfile').read_text()
        mine = mine[mine.index('FROM alpine'):]
        norm = lambda t: [l for l in t.splitlines() if l.strip() and not l.startswith('#')]
        a = [l.replace('COPY --from=build /src/listmonk .', 'COPY listmonk .') for l in norm(final)]
        self.assertEqual(a, norm(mine))

    def test_base_image_pins_match(self):
        axm_df = (ROOT / 'Dockerfile.axm').read_text()
        base = (HERE / 'base.Dockerfile').read_text()
        for pin in ('node:24.22.2-bookworm-slim', 'yarn@1.22.22'):
            self.assertIn(pin, axm_df)
            self.assertIn(pin, base)
        self.assertIn(axm.GO_IMAGE, axm_df)


class StampTest(unittest.TestCase):
    def test_changed_by_content_not_by_commit(self):
        stamp = {'files': {'a': 'h1', 'b': 'h2', 'gone': 'h3'}}
        files = ['a', 'b', 'new']
        hashes = {'a': 'h1', 'b': 'CHANGED', 'new': 'h4'}
        self.assertEqual(axm.changed_since_stamp(stamp, files, hashes), ['b', 'gone', 'new'])

    def test_no_change(self):
        stamp = {'files': {'a': 'h1'}}
        self.assertEqual(axm.changed_since_stamp(stamp, ['a'], {'a': 'h1'}), [])


class KeyTest(unittest.TestCase):
    def test_image_key_ignores_docs_and_specs(self):
        files = ['cmd/a.go', 'docs/x.md', 'frontend/cypress/e2e/a.cy.js', 'scripts/axm/axm']
        h1 = {f: '1' for f in files}
        h2 = dict(h1, **{'docs/x.md': '2', 'frontend/cypress/e2e/a.cy.js': '2', 'scripts/axm/axm': '2'})
        self.assertEqual(axm.image_key(files, h1), axm.image_key(files, h2))
        h3 = dict(h1, **{'cmd/a.go': '2'})
        self.assertNotEqual(axm.image_key(files, h1), axm.image_key(files, h3))

    def test_frontend_artifact_keys(self):
        files = ['frontend/src/a.vue', 'frontend/email-builder/src/b.tsx', 'frontend/tests/t.mjs', 'frontend/email-builder/yarn.lock']
        h = {f: '1' for f in files}
        eb, fe, ut = axm.art_keys(files, h)
        # 이메일 빌더 소스가 바뀌면 셋 다 바뀐다
        eb2, fe2, ut2 = axm.art_keys(files, dict(h, **{'frontend/email-builder/src/b.tsx': '2'}))
        self.assertTrue(eb != eb2 and fe != fe2 and ut != ut2)
        # 프론트 단위시험 파일만 바뀌면 dist 키는 그대로, 단위시험 키만 바뀐다
        eb3, fe3, ut3 = axm.art_keys(files, dict(h, **{'frontend/tests/t.mjs': '2'}))
        self.assertTrue(eb == eb3 and fe == fe3 and ut != ut3)


class ReviewFixesTest(unittest.TestCase):
    """LST-51 리뷰에서 나온 결함이 다시 생기지 않게 한다."""

    def test_tracking_check_spec_is_mapped(self):
        self.assertIn('campaign-tracking-check.cy.js', TMAP['ci_specs'])
        self.assertEqual(pick('frontend/cypress/e2e/campaign-tracking-check.cy.js')['specs'], ['campaign-tracking-check.cy.js'])
        self.assertIn('campaign-tracking-check.cy.js', pick('frontend/src/components/TrackingCheck.vue')['specs'])
        self.assertIn('campaign-tracking-check.cy.js', pick('cmd/axm_tracking_check.go')['specs'])

    def test_list_form_runs_draw_history_and_preset_specs(self):
        specs = pick('frontend/src/views/ListForm.vue')['specs']
        for s in ('list-draw.cy.js', 'subscriber-columns.cy.js', 'column-presets.cy.js', 'list-trash.cy.js'):
            self.assertIn(s, specs)

    def test_embedded_json_runs_core_tests_import_specs_and_unit(self):
        r = pick('internal/core/axm_standard_attribs.json')
        self.assertIn('internal/core', r['go'])
        self.assertIsNone(r['go']['internal/core'])
        self.assertIn('import-mapping.cy.js', r['specs'])
        self.assertTrue(r['unit'])

    def test_server_files_read_by_frontend_unit_tests_trigger_unit(self):
        self.assertTrue(pick('models/axm_draws.go')['unit'])
        self.assertTrue(pick('i18n/ko.json')['unit'])
        self.assertFalse(pick('cmd/axm_audit.go')['unit'])

    def test_unit_cache_key_includes_server_files(self):
        files = ['frontend/src/a.js', 'frontend/tests/t.mjs', 'models/axm_draws.go', 'i18n/en.json', 'cmd/x.go']
        h = {f: '1' for f in files}
        base = axm.art_keys(files, h)[2]
        self.assertNotEqual(base, axm.art_keys(files, dict(h, **{'models/axm_draws.go': '2'}))[2])
        self.assertNotEqual(base, axm.art_keys(files, dict(h, **{'i18n/en.json': '2'}))[2])

    def test_dependency_change_widens_cmd_even_with_a_feature_change(self):
        r = pick('internal/addrcheck/' + sorted(p.name for p in (ROOT / 'internal/addrcheck').glob('*.go') if not p.name.endswith('_test.go'))[0],
                 'cmd/axm_audit.go')
        self.assertIsNone(r['go']['cmd'])

    def test_failed_tests_are_not_hidden_by_a_narrower_selection(self):
        self.assertIsNone(axm.merge_go({'cmd': '^(TestA)$'}, {'cmd': None})['cmd'])
        self.assertIsNone(axm.merge_go({'cmd': None}, {'cmd': '^(TestA)$'})['cmd'])
        self.assertEqual(axm.merge_go({'cmd': '^(TestA)$'}, {'cmd': '^(TestB|TestC)$'})['cmd'], '^(TestA|TestB|TestC)$')
        self.assertEqual(axm.merge_go({'cmd': '^$'}, {'cmd': '^(TestB)$'})['cmd'], '^(TestB)$')
        self.assertEqual(axm.merge_go({'a': None}, {'b': '^$'}), {'a': None, 'b': '^$'})

    def test_free_port_never_returns_the_avoided_port(self):
        import socket
        sk = socket.socket()
        sk.bind(('127.0.0.1', 0))
        sk.listen()
        busy = sk.getsockname()[1]
        try:
            a = axm.free_port(busy)
            b = axm.free_port(busy + 1, avoid={a})
            self.assertNotEqual(a, b)
            self.assertNotEqual(a, busy)
        finally:
            sk.close()

    def test_image_key_follows_the_recipe(self):
        files = ['cmd/a.go']
        h = {'cmd/a.go': '1'}
        k = axm.image_key(files, h)
        orig = axm.recipe_digest
        try:
            axm.recipe_digest = lambda: 'changed'
            self.assertNotEqual(k, axm.image_key(files, h))
        finally:
            axm.recipe_digest = orig
        # 레시피 digest 는 두 Dockerfile 의 내용을 담는다.
        self.assertIn('base.Dockerfile', (HERE / 'axm').read_text().split('def recipe_digest')[1].split('def image_key')[0])

    def test_loopback_only_listeners(self):
        text = (HERE / 'axm').read_text()
        self.assertNotIn('0.0.0.0', text.replace("'0.0.0.0'", '', 0).split('def start_app')[1].split('def e2e')[0])
        self.assertIn('listen_addresses=127.0.0.1', text)

    def test_docker_build_gets_cpu_quota(self):
        self.assertIn('--cpu-quota', (HERE / 'axm').read_text().split('def docker_build')[1].split('def stuffbin_version')[0])


class StampFlowTest(unittest.TestCase):
    def test_first_round_failure_without_snapshot_falls_back_to_base(self):
        class A:
            since_last = True
            base = None
        orig_load, orig_base = axm.load_stamp, axm.changed_since_base
        try:
            axm.load_stamp = lambda ctx: {'ts': 1, 'failed_specs': ['audit.cy.js']}  # 첫 라운드 실패: files 없음
            axm.changed_since_base = lambda ref: ['frontend/src/views/Audit.vue']
            changed, st = axm.resolve_changed(A(), None, ['x'], {'x': 'h'})
            self.assertEqual(changed, ['frontend/src/views/Audit.vue'])
            self.assertEqual(st['failed_specs'], ['audit.cy.js'])
        finally:
            axm.load_stamp, axm.changed_since_base = orig_load, orig_base


class SpecCoverageTest(unittest.TestCase):
    """화면 파일이 선택하는 spec 이 그 화면의 data-cy 를 직접 단언하는 모든 CI spec 을 포함하는지 본다."""

    @staticmethod
    def _refs(text):
        return set(re.findall(r'data-cy[\^$*]?=["\']?([\w-]+)', text))

    def test_every_view_selects_the_specs_that_assert_its_data_cy(self):
        specs = {s: self._refs((ROOT / 'frontend/cypress/e2e' / s).read_text()) for s in TMAP['ci_specs']}
        missing = []
        vues = {f: set(re.findall(r'data-cy="([\w-]+)"', (ROOT / f).read_text())) for f in FILES
                if f.startswith('frontend/src/') and f.endswith('.vue')}
        uses = {}
        for v in vues.values():
            for x in v:
                uses[x] = uses.get(x, 0) + 1
        for f, mine in vues.items():
            mine = {x for x in mine if uses[x] == 1}  # 여러 화면이 같이 쓰는 이름(btn-save 등)은 제외
            chosen = set(pick(f)['specs'])
            for s, refs in specs.items():
                hit = any(r == v or (v.endswith('-') and r.startswith(v)) or (r.endswith('-') and v.startswith(r))
                          for r in refs for v in mine)
                if hit and s not in chosen:
                    missing.append(f'{f} -> {s}')
        self.assertEqual(missing, [], '화면이 data-cy 로 검사되는 spec 이 선택에서 빠졌다: test-map.json 에 추가하라')

    def test_every_axm_query_file_has_a_spec_rule(self):
        allowed = {'queries/axm_sends.sql'}  # 발송 엔진 전용이라 화면 spec 이 없다(Go 시험이 덮는다)
        for f in FILES:
            if re.match(r'queries/axm_[^/]*\.sql$', f) and f not in allowed:
                self.assertTrue(any(axm.match_any(f, r['paths']) for r in TMAP['rules']), f'{f} 규칙 없음')

    def test_reviewer_cases(self):
        self.assertIn('column-presets.cy.js', pick('frontend/src/views/Subscribers.vue')['specs'])
        self.assertIn('list-draw.cy.js', pick('queries/axm_draws.sql')['specs'])
        self.assertIn('list-trash.cy.js', pick('queries/axm_trash.sql')['specs'])

    def test_embedded_file_plus_feature_file_keeps_whole_package(self):
        r = pick('internal/core/axm_standard_attribs.json', 'internal/core/axm_audit.go')
        self.assertIsNone(r['go']['internal/core'])

    def test_parts_script_fails_on_setup_errors(self):
        text = (HERE / 'axm').read_text()
        text = text[text.index('PARTS_SH = r'):]
        for needle in ('AXM_FAIL rm', 'AXM_FAIL clean-state', 'stuffbin-missing'):
            self.assertIn(needle, text)


class RouteCoverageTest(unittest.TestCase):
    """화면이 선택하는 spec 이 그 화면을 실제로 방문하는 spec 과 같은지(빠짐도 넘침도 없게) 본다."""

    @staticmethod
    def _visits(spec):
        text = (ROOT / 'frontend/cypress/e2e' / spec).read_text()
        out = set(re.findall(r"(?:visit|loginAndVisit)\(\s*[`'\"]([^`'\"]*)", text))
        out |= {x.split('next=')[1] for x in out if 'next=' in x}
        return {x.split('?')[0] for x in out}

    def _visitors(self, pattern):
        rx = re.compile(pattern)
        return {s for s in TMAP['ci_specs'] if any(rx.match(v) for v in self._visits(s))}

    def test_subscribers_screens_select_exactly_the_specs_that_visit_them(self):
        want = self._visitors(r'^/admin/subscribers(?!/import)')
        for f in ('Subscribers.vue', 'SubscriberBulkList.vue', 'SubscriberForm.vue'):
            self.assertEqual(set(pick('frontend/src/views/' + f)['specs']), want, f)

    def test_campaign_editor_selects_every_spec_that_opens_it(self):
        want = self._visitors(r'^/admin/campaigns/(?!analytics)')
        self.assertLessEqual(want, set(pick('frontend/src/views/Campaign.vue')['specs']))
        self.assertIn('dynamic-lists.cy.js', pick('frontend/src/views/Campaign.vue')['specs'])

    def test_import_specs_are_not_pulled_in_by_subscriber_modals(self):
        self.assertNotIn('import-mapping.cy.js', pick('frontend/src/views/SubscriberBulkList.vue')['specs'])


if __name__ == '__main__':
    unittest.main()
