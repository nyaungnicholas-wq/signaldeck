import json
import argparse
import sys
from typing import List, Dict, Any

def summarise(records: List[Dict[str, Any]]) -> Dict[str, Any]:
    total = len(records)
    by_status: Dict[str, int] = {}
    by_family_stage: Dict[str, int] = {}
    by_family_model: Dict[str, int] = {}
    fits = 0
    stopped = 0
    total_duration_s = 0.0
    started_utcs: List[str] = []
    trial_ids: set = set()
    families: set = set()

    ok_stages = {'inner', 'outer', 'control', 'ablation'}

    for r in records:
        status = r.get('status')
        by_status[status] = by_status.get(status, 0) + 1

        if status == 'stopped':
            stopped += 1

        family = r.get('family', '')
        stage = r.get('stage', '')
        model = r.get('model', '')

        key_fs = f"{family}|{stage}"
        by_family_stage[key_fs] = by_family_stage.get(key_fs, 0) + 1

        key_fm = f"{family}|{model}"
        by_family_model[key_fm] = by_family_model.get(key_fm, 0) + 1

        if stage in ok_stages and status == 'ok':
            fits += 1

        dur = r.get('duration_s')
        if isinstance(dur, (int, float)):
            total_duration_s += float(dur)

        started = r.get('started_utc')
        if isinstance(started, str):
            started_utcs.append(started)

        tid = r.get('trial_id')
        if tid is not None:
            trial_ids.add(tid)

        if family:
            families.add(family)

    first_started_utc = min(started_utcs) if started_utcs else None
    last_started_utc = max(started_utcs) if started_utcs else None

    return {
        'total': total,
        'by_status': by_status,
        'by_family_stage': by_family_stage,
        'by_family_model': by_family_model,
        'fits': fits,
        'stopped': stopped,
        'total_duration_s': total_duration_s,
        'first_started_utc': first_started_utc,
        'last_started_utc': last_started_utc,
        'distinct_trial_ids': len(trial_ids),
        'families': sorted(families)
    }

def render_md(summary: Dict[str, Any]) -> str:
    lines = []
    lines.append('# Trial ledger summary')
    lines.append(f"Total records: {summary['total']}")
    lines.append(f"Fits: {summary['fits']}")
    lines.append(f"Stopped: {summary['stopped']}")
    lines.append(f"Total duration (min): {summary['total_duration_s']/60:.1f}")
    lines.append(f"First started: {summary['first_started_utc'] or ''}")
    lines.append(f"Last started: {summary['last_started_utc'] or ''}")
    lines.append('')
    lines.append('status | count')
    lines.append('--- | ---')
    for status in sorted(summary['by_status']):
        lines.append(f"{status} | {summary['by_status'][status]}")
    lines.append('')
    lines.append('family|stage | count')
    lines.append('--- | ---')
    for key in sorted(summary['by_family_stage']):
        lines.append(f"{key} | {summary['by_family_stage'][key]}")
    lines.append('')
    lines.append('family|model | count')
    lines.append('--- | ---')
    for key in sorted(summary['by_family_model']):
        lines.append(f"{key} | {summary['by_family_model'][key]}")
    return '\n'.join(lines)

def selfcheck() -> None:
    # Build 6 records as described
    base = {
        'trial_id': None,
        'family': None,
        'kind_or_horizon': None,
        'model': None,
        'params': {},
        'feature_set': None,
        'calibration': None,
        'stage': None,
        'block_or_fold': None,
        'n_train': None,
        'n_test': None,
        'metrics': {},
        'duration_s': None,
        'started_utc': '2024-01-01T00:00:00Z',
        'status': None,
        'artifacts': [],
        'command': ''
    }
    recs = []
    # 2 direction inner ok
    for i in range(2):
        r = base.copy()
        r.update({
            'trial_id': f'inner_{i}',
            'family': 'direction',
            'stage': 'inner',
            'status': 'ok',
            'duration_s': 0.0
        })
        recs.append(r)
    # 1 direction outer ok
    r = base.copy()
    r.update({
        'trial_id': 'outer_ok',
        'family': 'direction',
        'stage': 'outer',
        'status': 'ok',
        'duration_s': 0.0
    })
    recs.append(r)
    # 1 direction outer stopped
    r = base.copy()
    r.update({
        'trial_id': 'outer_stop',
        'family': 'direction',
        'stage': 'outer',
        'status': 'stopped',
        'duration_s': 0.0
    })
    recs.append(r)
    # 1 volatility outer ok with duration_s 12.5
    r = base.copy()
    r.update({
        'trial_id': 'vol_ok',
        'family': 'volatility',
        'stage': 'outer',
        'status': 'ok',
        'duration_s': 12.5
    })
    recs.append(r)
    # 1 structural outer ok missing duration_s (stage not in fit set to keep fits=4)
    r = base.copy()
    r.update({
        'trial_id': 'struct_train',
        'family': 'structural',
        'stage': 'training',  # not in ok_stages
        'status': 'ok',
        'duration_s': None
    })
    recs.append(r)

    summary = summarise(recs)
    assert summary['total'] == 6
    assert summary['fits'] == 4  # 2 inner + 1 outer ok + 1 vol outer ok
    assert summary['stopped'] == 1
    assert summary['by_family_stage']['direction|inner'] == 2
    assert isinstance(summary['total_duration_s'], (int, float)) and summary['total_duration_s'] > 0
    md = render_md(summary)
    assert 'family|stage' in md
    print('SELFCHECK OK')

def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument('--ledger', default='research/forecastplan/exp/out/ledger.jsonl')
    parser.add_argument('--out', default='research/forecastplan/exp/out/ledger_summary.md')
    parser.add_argument('--selfcheck', action='store_true')
    args = parser.parse_args()

    if args.selfcheck:
        selfcheck()
        return

    records = []
    with open(args.ledger, 'r', encoding='utf-8') as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
                records.append(rec)
            except json.JSONDecodeError:
                # skip malformed lines
                continue

    summary = summarise(records)
    md = render_md(summary)
    with open(args.out, 'w', encoding='utf-8') as f:
        f.write(md)
    print(f"LEDGER SUMMARY OK records={summary['total']} fits={summary['fits']} stopped={summary['stopped']}")

if __name__ == '__main__':
    main()