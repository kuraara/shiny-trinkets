"""Unit tests for align.py (no speech models needed): python3 -m unittest test_align"""
import unittest
from align import align, cut


def words_for(text, t0, gap=0.0, dur=0.3):
    out, t = [], t0
    for w in text.split():
        out.append({'w': w.lower().strip('.,!?'), 'start': round(t, 2), 'end': round(t + dur, 2)})
        t += dur + gap
    return out


class AlignTest(unittest.TestCase):
    def test_late_subtitles_and_cut(self):
        # speech is 1.5 s after the subtitles; one word misheard at the end of line 2
        lines = [{'text': 'My fingers are still sticky.', 'start': 1.0, 'end': 2.5},
                 {'text': 'You can suck on them if you want.', 'start': 3.0, 'end': 4.5},
                 {'text': 'Well, I will be upstairs', 'start': 7.0, 'end': 8.0}]
        words = words_for('my fingers are still sticky', 2.5) + words_for('you can suck on them if you wont', 4.5) \
            + words_for('well i will be upstairs', 8.6)
        al, offset, n = align(words, lines)
        self.assertAlmostEqual(offset, 1.5, delta=0.4)
        self.assertGreater(n, 12)
        self.assertAlmostEqual(al[1]['start'], 4.5, delta=0.05)
        # the misheard last word still ends inside the line
        self.assertGreaterEqual(al[1]['end'] + 0.01, 4.5 + 7 * 0.3)
        c = cut(al, words, 0, 1, 0.5, 0.7, 20.0)
        self.assertAlmostEqual(c['speech_start'], 2.5, delta=0.05)
        self.assertAlmostEqual(c['speech_end'], 4.5 + 8 * 0.3, delta=0.05)   # "wont" included
        self.assertLess(c['end'], 8.6)                                          # not into the next line
        self.assertGreater(c['end'], c['speech_end'])

    def test_no_match_falls_back_to_offset(self):
        lines = [{'text': 'Completely different words here', 'start': 1.0, 'end': 2.0}]
        al, offset, n = align(words_for('nothing alike at all', 1.2), lines)
        self.assertEqual(n, 0)
        self.assertEqual((al[0]['start'], al[0]['end']), (1.0, 2.0))

    def test_cut_stops_at_next_line_when_speech_runs_on(self):
        lines = [{'text': 'need me', 'start': 0.0, 'end': 0.6}, {'text': 'Steve you are', 'start': 0.8, 'end': 1.7}]
        words = words_for('need me', 0.0) + words_for('steve you are', 0.62)
        al, _, _ = align(words, lines)
        c = cut(al, words, 0, 0, 0.5, 0.7, 10.0)
        # stops at the next line, apart from the minimum 0.2 s (the clip's sound fades out over it)
        self.assertAlmostEqual(c['end'], c['speech_end'] + 0.2, delta=0.01)


if __name__ == '__main__':
    unittest.main()
