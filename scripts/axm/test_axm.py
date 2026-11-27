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
        # campaign-test-send 도 감사 화면(/admin/settings/audit)과 감사 API 를 단언한다.
        self.assertEqual(r['specs'], ['audit.cy.js', 'campaign-test-send.cy.js', 'campaign-delete-guard.cy.js'])
        self.assertTrue(r['unit'])

    def test_backend_feature_file(self):
        r = pick('cmd/axm_audit.go')
        self.assertEqual(r['specs'], ['audit.cy.js', 'campaign-test-send.cy.js', 'campaign-delete-guard.cy.js'])

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
        r = pick('frontend/src/views/Settings.vue')
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
        # 고르는 것은 파일이 없는 CI spec 뿐이다(정상 트리에서는 없음 — test_all_spec_files_exist 가 따로 본다).
        missing = [s for s in TMAP['ci_specs'] if not (ROOT / 'frontend/cypress/e2e' / s).is_file()]
        self.assertEqual((r['specs'], r['go'], r['full_build']), (missing, {}, False))
        self.assertTrue(r['python'])  # spec 목록이 바뀌면 맵 일치 시험을 돌린다

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
        # 워크플로의 `cypress run` 명령 전부(두 번째 명령 포함)를 읽는다.
        ci = axm.workflow_specs()
        self.assertTrue(ci, '워크플로에서 cypress run 명령을 못 찾았다')
        self.assertEqual(ci, TMAP['map_ci_specs'], 'test-map.json 의 ci_specs 가 axm-image.yml 의 cypress run 들과 다르다')

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
        # 리뷰 F28: 기준 브랜치(axm/v6.2)가 없는 단일 브랜치 복제본에서도 돌아야 한다 — 기준 참조도 대역으로 바꾼다.
        orig_load, orig_base, orig_ref = axm.load_stamp, axm.changed_since_base, axm.base_ref
        try:
            axm.load_stamp = lambda ctx: {'ts': 1, 'failed_specs': ['audit.cy.js']}  # 첫 라운드 실패: files 없음
            axm.changed_since_base = lambda ref: ['frontend/src/views/Audit.vue'] if ref == 'BASE' else ['wrong']
            axm.base_ref = lambda arg: 'BASE'
            changed, st = axm.resolve_changed(A(), None, ['x'], {'x': 'h'})
            self.assertEqual(changed, ['frontend/src/views/Audit.vue'])
            self.assertEqual(st['failed_specs'], ['audit.cy.js'])
        finally:
            axm.load_stamp, axm.changed_since_base, axm.base_ref = orig_load, orig_base, orig_ref


class Round6Test(unittest.TestCase):
    """리뷰 6차 지적(F25~F30) 회귀 시험."""

    def test_config_sample_runs_smoke(self):
        # F25: config.toml.sample 은 앱 설치(--install)가 읽는다. 시험 0개로 통과하면 안 된다.
        r = pick('config.toml.sample')
        self.assertEqual(set(r['specs']), set(TMAP['smoke']))

    def test_no_tracked_file_selects_nothing_silently(self):
        # F25 일반화: 문서·도구 목록(none)에 없는 파일은 무엇이든 고르거나, 건너뛰는 이유를 적는다.
        for f in FILES:
            r = pick(f)
            if r['docs_only'] or r['specs'] or r['go'] or r['unit'] or r['python'] or r['full_build']:
                continue
            self.assertTrue(any('건너뜀' in n for n in r['notes']), f)

    def test_unknown_file_warns_and_runs_smoke(self):
        r = pick('some-new-root-file.cfg')
        self.assertEqual(r['unmapped'], ['some-new-root-file.cfg'])
        self.assertEqual(set(r['specs']), set(TMAP['smoke']))

    def test_fixtures_select_the_specs_that_read_them(self):
        # F26: 픽스처를 바꾸면 그 데이터를 검증하는 spec 이 걸려야 한다.
        self.assertIn('reaction-lists.cy.js', pick('frontend/cypress/fixtures/reaction-lists.sql')['specs'])
        for csv in ('import-map-cp949.csv', 'import-map-utf8-bom.csv', 'import-map-broken.csv'):
            self.assertIn('import-mapping.cy.js', pick('frontend/cypress/fixtures/' + csv)['specs'], csv)
        e2e = ROOT / 'frontend/cypress/e2e'
        for fx in sorted((ROOT / 'frontend/cypress/fixtures').iterdir()):
            users = [s for s in TMAP['ci_specs'] if fx.name in (e2e / s).read_text()]
            sel = pick('frontend/cypress/fixtures/' + fx.name)['specs']
            self.assertLessEqual(set(users), set(sel), fx.name)

    def test_fixture_used_only_by_upstream_specs_is_skipped_with_a_note(self):
        r = pick('frontend/cypress/fixtures/logo.png')
        self.assertEqual(r['specs'], [])
        self.assertTrue(any('건너뜀' in n for n in r['notes']))

    def test_union_of_feature_files_never_runs_the_whole_ci_suite(self):
        # F27: 기능 파일 셋의 합이 CI 전체가 되어도 로컬은 전체를 돌리지 않는다.
        r = pick('cmd/campaigns.go', 'cmd/subscribers.go', 'frontend/src/views/ListForm.vue', 'frontend/src/views/Bounces.vue')
        self.assertNotEqual(set(r['specs']), set(TMAP['ci_specs']))
        self.assertEqual(set(r['specs']), set(TMAP['smoke']))
        self.assertEqual(set(r['specs']) | set(r['deferred_specs']), set(TMAP['ci_specs']))
        # 바뀐 spec 은 로컬에서 돈다.
        r = pick('cmd/campaigns.go', 'cmd/subscribers.go', 'frontend/src/views/ListForm.vue', 'frontend/src/views/Bounces.vue',
                 'frontend/cypress/e2e/reaction-lists.cy.js')
        self.assertIn('reaction-lists.cy.js', r['specs'])
        self.assertNotEqual(set(r['specs']), set(TMAP['ci_specs']))

    def test_previous_failures_do_not_widen_to_the_whole_suite(self):
        class A:
            since_last = True
            base = None
        orig = axm.resolve_changed
        try:
            axm.resolve_changed = lambda a, ctx, files, hashes: (
                ['cmd/campaigns.go', 'cmd/subscribers.go'],
                {'files': {'x': 'h'}, 'failed_specs': ['list-draw.cy.js', 'dynamic-lists.cy.js']})
            _, res, _ = axm.select_tests(A(), None, FILES, {})
        finally:
            axm.resolve_changed = orig
        self.assertNotEqual(set(res['specs']), set(TMAP['ci_specs']))
        self.assertLessEqual({'list-draw.cy.js', 'dynamic-lists.cy.js'}, set(res['specs']))

    def test_go_failures_are_only_the_failed_packages(self):
        # F29: 실패 기록에 통과한 패키지를 넣지 않는다.
        mod = axm.go_module()
        text = f'ok  \t{mod}/internal/a\t0.1s\nFAIL\t{mod}/internal/b [setup failed]\nFAIL'
        self.assertEqual(axm.go_failures({'internal/a': None, 'internal/b': '^(TestX)$'}, text, 1), {'internal/b': '^(TestX)$'})
        self.assertEqual(axm.go_failures({'internal/a': None}, text, 0), {})
        self.assertEqual(axm.go_failures({'cmd': None}, 'build failed', 1), {'cmd': None})

    def test_deleted_failed_package_is_not_retried(self):
        st = {'failed_go': {'internal/gone': None, 'internal/core': None}}
        self.assertEqual(axm.live_failed_go(st, FILES), {'internal/core': None})
        specs, go = axm.failure_record(st, [], [], False, {'cmd': None}, {}, True, FILES)
        self.assertEqual(go, {'internal/core': None})  # 지운 패키지는 빠지고, 돌지 못한 실패는 남는다
        specs, go = axm.failure_record(st, [], [], False, {'internal/core': None}, {}, True, FILES)
        self.assertEqual(go, {})  # 이번에 통과했으면 빠진다
        specs, go = axm.failure_record({'failed_specs': ['audit.cy.js', 'list-draw.cy.js']}, ['audit.cy.js'], [], True, {}, {}, False, FILES)
        self.assertEqual(specs, ['list-draw.cy.js'])

    def test_runtime_build_cpu_is_unmeasured_not_zero(self):
        # F30: 단계 줄이 없으면 CPU 는 None('-'), -q 로 단계 줄을 지우지 않는다.
        text = (HERE / 'axm').read_text()
        rt = text.split("docker_build(ctx, 'runtime'")[1].split('\n')[0]
        self.assertNotIn("'-q'", rt)
        class T:
            steps, cpu = {}, {}
        self.assertIsNone(axm.build_result(T(), [], 0, 1.0, None)['cpu'])
        T.steps = {1: dict(cmd='RUN apk', ids=['abc'], cached=False), 2: dict(cmd='COPY', ids=[], cached=False)}
        T.cpu = {}
        self.assertIsNone(axm.build_result(T(), [(1, 'RUN', 1.0, 0.0), (2, 'COPY', 0.1, 0.0)], 0, 1.0, None)['cpu'])
        T.cpu = {'abc': 2.5}
        self.assertEqual(axm.build_result(T(), [(1, 'RUN', 1.0, 2.5), (2, 'COPY', 0.1, 0.0)], 0, 1.0, None)['cpu'], 2.5)
        T.steps = {1: dict(cmd='RUN apk', ids=[], cached=True)}
        r = axm.build_result(T(), [(1, 'RUN', 0.1, 0.0)], 0, 1.0, None)
        self.assertEqual((r['cpu'], r['cached']), (0.0, 1))

    def test_build_cpu_comes_from_the_build_slice(self):
        # 짧은 RUN 컨테이너는 폴링이 놓친다 → 빌드 컨테이너를 모은 slice 의 누적값을 쓴다.
        class T:
            steps = {1: dict(cmd='RUN chmod', ids=['abc'], cached=False)}
            cpu = {}
        r = axm.build_result(T(), [(1, 'RUN', 0.2, 0.0)], 0, 1.0, None, slice_cpu=0.8)
        self.assertEqual(r['cpu'], 0.8)
        text = (HERE / 'axm').read_text().split('def docker_build')[1].split('def build_result')[0]
        self.assertIn("'--cgroup-parent'", text)
        self.assertEqual(axm.slice_path('axm-x.slice').name, 'axm-x.slice')

    def test_build_tracker_reads_cache_lines(self):
        tr = axm.BuildTracker()
        for line in ['Step 1/3 : FROM alpine', ' ---> 1234', 'Step 2/3 : RUN apk add x', ' ---> Using cache',
                     'Step 3/3 : COPY a b', ' ---> Running in 0123456789ab']:
            tr.feed(line)
        rows = tr.done()
        self.assertEqual(len(rows), 3)
        self.assertTrue(tr.steps[2].get('cached'))
        self.assertEqual(tr.steps[3]['ids'], ['0123456789ab'])


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
        out = set(re.findall(r"\b(?:visit|loginAndVisit|visitWithFixedClock)\(\s*[`'\"]([^`'\"]*)", text))
        # 클릭으로 옮겨 간 화면도 방문이다: cy.location('pathname').should('eq', '/admin/…')
        out |= set(re.findall(r"location\(\s*'pathname'\s*\)\.should\(\s*'eq',\s*[`'\"]([^`'\"]*)", text))
        out |= {x.split('next=')[1] for x in out if 'next=' in x}
        return {x.split('?')[0].split('#')[0] for x in out}

    def _visitors(self, pattern):
        rx = re.compile(pattern)
        return {s for s in TMAP['ci_specs'] if any(rx.match(v) for v in self._visits(s))}

    @staticmethod
    def _api_calls(spec, base):
        """spec 이 cy.request·cy.intercept 로 부르는 base 아래 API 경로(메서드 포함 원문)."""
        text = (ROOT / 'frontend/cypress/e2e' / spec).read_text()
        rx = (r"\b(?:request|intercept)\(\s*(?:\{[^}]*?url:\s*)?(?:'[A-Z]+',\s*)?[`'\"]"
              r"(?:\$\{[^}]*\})?(" + re.escape(base) + r"[^`'\"]*)")
        calls = []
        for m in re.finditer(rx, text):
            fixture = re.match(r"(?:request|intercept)\(\s*'POST',\s*[`'\"]" + re.escape(base) + "[`'\"]", m.group(0) + "'")
            calls.append(('create' if fixture else '') + m.group(1))
        return calls

    def test_subscribers_screens_select_exactly_the_specs_that_visit_them(self):
        want = self._visitors(r'^/admin/subscribers(?!/import|/bounces)')
        for f in ('Subscribers.vue', 'SubscriberBulkList.vue', 'SubscriberForm.vue'):
            self.assertEqual(set(pick('frontend/src/views/' + f)['specs']), want, f)

    def test_campaign_editor_selects_exactly_the_specs_that_open_it(self):
        want = self._visitors(r'^/admin/campaigns/(?!analytics|media|templates)')
        self.assertEqual(set(pick('frontend/src/views/Campaign.vue')['specs']), want)
        self.assertIn('dynamic-lists.cy.js', want)

    def test_campaign_list_selects_exactly_the_specs_that_open_it(self):
        # 리뷰 F24: Campaigns.vue(/admin/campaigns)만 바꿨는데 /admin/campaigns/new 만 여는 dynamic-lists 가 끌려왔다.
        want = self._visitors(r'^/admin/campaigns$')
        self.assertEqual(set(pick('frontend/src/views/Campaigns.vue')['specs']), want)
        self.assertNotIn('dynamic-lists.cy.js', want)

    def test_subscriber_api_selects_every_spec_that_uses_it_beyond_fixtures(self):
        # 리뷰 F23: cmd/subscribers.go 의 일괄 구독 해지 감사 인원을 검증하는 address-check 가 빠졌다.
        # 기준: 화면 방문 spec + /api/subscribers 를 만들기(POST 픽스처) 말고도 부르는 spec.
        # 다른 파일의 경로(address-check → axm_address_check.go, bounces → bounces.go)는 뺀다.
        def uses(spec):
            return any(not c.startswith('create') and '/address-check' not in c and '/bounces' not in c
                       for c in self._api_calls(spec, '/api/subscribers'))
        want = self._visitors(r'^/admin/subscribers(?!/import|/bounces)') | {s for s in TMAP['ci_specs'] if uses(s)}
        self.assertIn('address-check.cy.js', want)
        for f in ('cmd/subscribers.go', 'internal/core/subscribers.go'):
            self.assertEqual(set(pick(f)['specs']), want, f)

    def test_campaign_api_selects_every_spec_that_uses_it(self):
        # 같은 기준: 캠페인 API 를 부르는 spec + 캠페인 데이터를 불러오는 화면(목록, /admin/campaigns/<id>)을 여는 spec.
        # /admin/campaigns/new 는 캠페인 API 를 부르지 않는다(목록 선택만 보는 dynamic-lists 는 빠진다).
        want = self._visitors(r'^/admin/campaigns(?:$|/(?!new$|analytics|media|templates))')
        want |= {s for s in TMAP['ci_specs'] if self._api_calls(s, '/api/campaigns')}
        for f in ('cmd/campaigns.go', 'internal/core/campaigns.go'):
            self.assertEqual(set(pick(f)['specs']), want, f)

    def test_import_specs_are_not_pulled_in_by_subscriber_modals(self):
        self.assertNotIn('import-mapping.cy.js', pick('frontend/src/views/SubscriberBulkList.vue')['specs'])


class Round7Test(unittest.TestCase):
    """리뷰 7차 지적 회귀 시험."""

    def test_stale_map_still_runs_a_changed_ci_spec_and_fails_the_check(self):
        # 기준 브랜치 병합으로 CI 에 spec 이 늘었는데 맵이 그대로면: 그 spec 을 건너뛰지 않고, 맵 시험을 돌려 검사가 실패한다.
        raw = json.loads((HERE / 'test-map.json').read_text())
        raw['ci_specs'] = [x for x in raw['ci_specs'] if x != 'campaign-test-send.cy.js']
        stale = axm.reconcile_ci(raw, axm.workflow_specs())
        self.assertEqual(stale['stale_ci'], ['campaign-test-send.cy.js'])
        r = axm.affected(['frontend/cypress/e2e/campaign-test-send.cy.js'], FILES, stale)
        self.assertEqual(r['specs'], ['campaign-test-send.cy.js'])
        self.assertTrue(r['python'])
        self.assertTrue(any('ci_specs' in n for n in r['notes']))

    def test_test_send_feature_files_select_its_spec(self):
        for f in ('frontend/src/components/CampaignTestSend.vue', 'frontend/src/test-send.js', 'cmd/axm_test_send.go',
                  'internal/core/axm_test_address.go', 'frontend/cypress/e2e/campaign-test-send.cy.js'):
            self.assertIn('campaign-test-send.cy.js', pick(f)['specs'], f)

    def test_narrowing_never_drops_changed_or_failed_specs(self):
        ci, smoke = TMAP['ci_specs'], TMAP['smoke']
        others = [s for s in ci if s not in smoke]
        # 바뀐 spec + 지난 실패가 smoke 와 합쳐 CI 전부가 되어도 필수 spec 은 남는다.
        specs, deferred = axm.local_specs(ci, others, TMAP)
        self.assertLessEqual(set(others), set(specs))
        # 필수 spec 만으로 CI 전부면 전부 돈다.
        specs, deferred = axm.local_specs(ci, ci, TMAP)
        self.assertEqual((specs, deferred), (ci, []))
        # 필수가 없으면 smoke 만.
        self.assertEqual(set(axm.local_specs(ci, [], TMAP)[0]), set(smoke))

    def test_reviewer_case_failed_fixture_spec_is_retried(self):
        # 리뷰 재현: reaction-lists 가 실패한 채 다른 비-smoke spec 9개에 주석만 단 재검사 → reaction-lists 가 다시 돈다.
        ci, smoke = TMAP['ci_specs'], TMAP['smoke']
        nine = [s for s in ci if s not in smoke and s != 'reaction-lists.cy.js']
        changed = ['frontend/cypress/fixtures/reaction-lists.sql'] + ['frontend/cypress/e2e/' + s for s in nine]
        class A:
            since_last = True
            base = None
        orig = axm.resolve_changed
        try:
            axm.resolve_changed = lambda a, ctx, files, hashes: (
                changed, {'files': {'x': 'h'}, 'failed_specs': ['reaction-lists.cy.js']})
            _, res, _ = axm.select_tests(A(), None, FILES, {})
        finally:
            axm.resolve_changed = orig
        self.assertIn('reaction-lists.cy.js', res['specs'])
        self.assertEqual(res['retried_specs'], ['reaction-lists.cy.js'])
        self.assertLessEqual(set(nine), set(res['specs']))
        # 실패 기록은 다시 돌아 통과하기 전에는 지워지지 않는다.
        specs, _ = axm.failure_record({'failed_specs': ['reaction-lists.cy.js']}, res['specs'], [], False, {}, {}, False, FILES)
        self.assertEqual(specs, ['reaction-lists.cy.js'])

    def test_deferred_specs_are_recorded_until_they_pass_locally(self):
        st = {'deferred_specs': ['list-draw.cy.js', 'audit.cy.js']}
        self.assertEqual(axm.deferred_record(st, ['audit.cy.js'], ['dynamic-lists.cy.js'], True),
                         ['dynamic-lists.cy.js', 'list-draw.cy.js'])
        self.assertEqual(axm.deferred_record(st, ['audit.cy.js'], [], False), ['list-draw.cy.js', 'audit.cy.js'])


class Round8Test(unittest.TestCase):
    """리뷰 8차 지적 회귀 시험."""

    @staticmethod
    def _first_command(wf):
        """첫 `cypress run` 명령의 논리적 범위(역슬래시로 이은 다음 줄들까지)와 들여쓰기."""
        m = re.search(r"^( *)[^\n]*\bcypress\s+run\b[^\n]*\n", wf, re.M)
        end = m.end()
        while wf[:end].rstrip('\n').endswith('\\'):
            end = wf.index('\n', end) + 1
        return m.start(), end, m.group(1)

    def _variants(self):
        """실제 워크플로와, 같은 spec 을 같은 순서로 두 명령에 나누고 첫 명령의 --spec 을 다음 줄로 이은 변형(리뷰 재현)."""
        wf = (ROOT / axm.CI_WORKFLOW).read_text()
        ci = TMAP['map_ci_specs']
        arg = lambda xs: ','.join('cypress/e2e/' + x for x in xs)
        a, b, ind = self._first_command(wf)
        split = (f"{ind}npx cypress run --browser chrome \\\n{ind}  --spec '{arg(ci[:-2])}'\n"
                 f"{ind}npx cypress run --browser chrome --spec '{arg(ci[-2:])}'\n")
        return {'실제': wf, '나뉨+줄 이음': wf[:a] + split + wf[b:]}

    def test_workflow_reads_every_cypress_command(self):
        for name, wf in self._variants().items():
            self.assertEqual(axm.workflow_specs(wf), TMAP['map_ci_specs'], name)
            # 첫 명령(이어 쓴 줄까지) 바로 뒤에 새 명령을 넣는다.
            a, b, ind = self._first_command(wf)
            extra = f"{ind}npx cypress run --browser chrome --spec 'cypress/e2e/campaign-extra.cy.js'\n"
            specs = axm.workflow_specs(wf[:b] + extra + wf[b:])
            # 순서는 명령이 나오는 순서다: 첫 명령의 spec 들 → 넣은 명령 → 그 뒤 명령들.
            first = axm.workflow_specs(wf[a:b])
            rest = [x for x in TMAP['map_ci_specs'] if x not in first]
            self.assertEqual(specs, first + ['campaign-extra.cy.js'] + rest, name)
            # 맵이 그 spec 을 모르면 뒤처진 맵으로 잡힌다.
            raw = json.loads((HERE / 'test-map.json').read_text())
            self.assertEqual(axm.reconcile_ci(raw, specs)['stale_ci'], ['campaign-extra.cy.js'], name)

    def test_split_ci_with_the_same_order_matches_the_map(self):
        # 리뷰 재현: 기존 CI spec 을 같은 순서로 두 명령(14+나머지)에 나누면 맵과 그대로 같아야 한다.
        ci = TMAP['map_ci_specs']
        arg = lambda xs: ','.join('cypress/e2e/' + x for x in xs)
        for k in (1, len(ci) - 2, len(ci) - 1):
            t = (f"          npx cypress run --browser chrome --spec '{arg(ci[:k])}'\n"
                 f"          npx cypress run --browser chrome --spec '{arg(ci[k:])}'\n")
            self.assertEqual(axm.workflow_specs(t), ci, k)
            self.assertEqual(axm.reconcile_ci(json.loads((HERE / 'test-map.json').read_text()), axm.workflow_specs(t))['stale_ci'], [])

    def test_commands_chained_on_one_line(self):
        # 리뷰 재현: 두 Cypress 명령을 && 로 이으면 두 번째 명령의 spec 도 CI spec 이다.
        ci = TMAP['map_ci_specs']
        arg = ','.join('cypress/e2e/' + x for x in ci)
        for sep in (' && ', '; ', ' || '):
            t = f"          npx cypress run --browser chrome --spec '{arg}'{sep}npx cypress run --spec 'cypress/e2e/review-smoke.cy.js'\n"
            self.assertEqual(axm.workflow_specs(t), ci + ['review-smoke.cy.js'], sep)
        t = "  yarn cypress run -s cypress/e2e/x.cy.js; cypress run --spec=\"cypress/e2e/y.cy.js\" | tee log"
        self.assertEqual(axm.workflow_specs(t), ['x.cy.js', 'y.cy.js'])
        # 그 spec 을 맵이 모르면 뒤처진 맵이고, 그 spec 파일은 CI spec 으로 골라진다(건너뜀 아님).
        specs = axm.workflow_specs(f"npx cypress run --spec '{arg}' && npx cypress run --spec 'cypress/e2e/schedule-time.cy.js'")
        raw = json.loads((HERE / 'test-map.json').read_text())
        raw['ci_specs'] = [x for x in raw['ci_specs'] if x != 'schedule-time.cy.js']
        stale = axm.reconcile_ci(raw, specs)
        self.assertEqual(stale['stale_ci'], ['schedule-time.cy.js'])
        r = axm.affected(['frontend/cypress/e2e/schedule-time.cy.js'], FILES, stale)
        self.assertEqual((r['specs'], r['python']), (['schedule-time.cy.js'], True))

    def test_schedule_time_screens_select_its_spec(self):
        for f in ('frontend/src/components/TimezonePicker.vue', 'frontend/src/components/TimezoneRow.vue',
                  'frontend/src/schedule-time.js', 'cmd/axm_send_tz.go', 'frontend/src/views/Campaign.vue',
                  'frontend/src/views/Campaigns.vue', 'cmd/campaigns.go'):
            self.assertIn('schedule-time.cy.js', pick(f)['specs'], f)

    def test_workflow_parser_forms(self):
        t = '''
          npx cypress run --spec "cypress/e2e/a.cy.js,cypress/e2e/b.cy.js"
          npx cypress run --browser chrome \\
            --spec=cypress/e2e/c.cy.js
          npx cypress run --spec 'cypress/e2e/audit*.cy.js'
        '''
        self.assertEqual(axm.workflow_specs(t), ['a.cy.js', 'b.cy.js', 'c.cy.js', 'audit.cy.js'])
        everything = sorted(p.name for p in (ROOT / 'frontend/cypress/e2e').glob('*.cy.js'))
        self.assertEqual(axm.workflow_specs('run: npx cypress run --browser chrome'), everything)
        self.assertIsNone(axm.workflow_specs('no cypress here'))

    def test_renamed_ci_spec_is_selected_and_counted_as_failed(self):
        # 리뷰 재현 1: CI 목록의 spec 파일 이름을 바꾸면(원래 이름 파일 없음) 그 spec 을 고르고, 결과가 없어 실패다.
        raw = json.loads((HERE / 'test-map.json').read_text())
        raw = axm.reconcile_ci(raw, axm.workflow_specs())
        raw['ci_specs'] = raw['ci_specs'] + ['gone-renamed.cy.js']
        r = axm.affected(['frontend/cypress/e2e/renamed.cy.js'], FILES, raw)
        self.assertIn('gone-renamed.cy.js', r['specs'])
        self.assertTrue(r['python'])
        self.assertTrue(any('파일이 없다' in n for n in r['notes']))
        # 줄여도 빠지지 않는다.
        r = axm.affected(['cmd/campaigns.go', 'cmd/subscribers.go', 'frontend/src/views/ListForm.vue', 'frontend/src/views/Bounces.vue'], FILES, raw)
        self.assertIn('gone-renamed.cy.js', r['specs'])

    def test_spec_without_a_result_row_is_failed(self):
        # 리뷰 재현 2: 2개를 골랐는데 Cypress 가 1개만 돌고 0 으로 끝나도 통과가 아니다.
        ok, passed, failed = axm.judge_e2e(['audit.cy.js', 'campaign-test-send.cy.js'], True, ['audit.cy.js'], [])
        self.assertEqual((ok, passed, failed), (False, ['audit.cy.js'], ['campaign-test-send.cy.js']))
        ok, passed, failed = axm.judge_e2e(['audit.cy.js'], True, ['audit.cy.js'], [])
        self.assertTrue(ok)
        # 통과 기록의 ran_specs 는 실제로 통과한 spec 이다.
        text = (HERE / 'axm').read_text().split('def cmd_check')[1].split('def failure_record')[0]
        self.assertIn('ran_specs=ran_specs', text)


class Round11Test(unittest.TestCase):
    def test_from_addresses_screens_select_its_spec(self):
        # 리뷰 재현: 기준 브랜치 병합(#39)으로 들어온 from-addresses.cy.js 를 캠페인 작성·발신 주소 설정·캠페인 API 가 고른다.
        for f in ('frontend/src/views/Campaign.vue', 'frontend/src/views/Campaigns.vue',
                  'frontend/src/views/settings/general.vue', 'cmd/campaigns.go', 'cmd/senders.go',
                  'cmd/settings.go', 'cmd/admin.go', 'frontend/cypress/e2e/from-addresses.cy.js'):
            self.assertIn('from-addresses.cy.js', pick(f)['specs'], f)


if __name__ == '__main__':
    unittest.main()


class StartAppCleanupTest(unittest.TestCase):
    """LST-48 review: when the app does not start after the test inbox did, neither is left behind."""

    def test_failed_start_removes_app_and_mail(self):
        calls = []

        class FakeCtx:
            def name(self, what):
                return f'axm0-{what}'

        def fail(*a, **k):
            raise axm.StageFailed('앱이 /health 에 답하지 않는다')

        old_docker, old_start = axm.docker, axm._start_app
        axm.docker = lambda *a, **k: calls.append(a)
        axm._start_app = fail
        try:
            with self.assertRaises(axm.StageFailed):
                axm.start_app(FakeCtx(), None, 'img', None)
        finally:
            axm.docker, axm._start_app = old_docker, old_start
        removed = {a[2] for a in calls if a[:2] == ('rm', '-f')}
        self.assertEqual(removed, {'axm0-app', 'axm0-mail'})
