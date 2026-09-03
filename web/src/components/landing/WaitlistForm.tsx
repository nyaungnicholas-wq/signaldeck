"use client";
import React, { useState } from 'react';

export default function WaitlistForm() {
  const [email, setEmail] = useState<string>('');
  const [state, setState] = useState<'idle' | 'sending' | 'ok' | 'error'>('idle');
  const [message, setMessage] = useState<string>('');

  const handleSubmit = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const form = e.currentTarget;
    const hpInput = form.elements.namedItem('hp') as HTMLInputElement | null;
    const hp = hpInput?.value ?? '';

    const trimmed = email.trim().toLowerCase();

    if (!trimmed) {
      setState('error');
      setMessage('Please enter an email address.');
      return;
    }
    if (trimmed.length > 254) {
      setState('error');
      setMessage('Email address is too long.');
      return;
    }
    const atCount = (trimmed.match(/@/g) || []).length;
    if (atCount !== 1) {
      setState('error');
      setMessage('Please enter a valid email address.');
      return;
    }
    const [local, domain] = trimmed.split('@');
    if (!local || !domain) {
      setState('error');
      setMessage('Please enter a valid email address.');
      return;
    }

    setState('sending');
    setMessage('');

    try {
      const res = await fetch('/api/waitlist', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Signaldeck': '1',
        },
        credentials: 'include',
        body: JSON.stringify({ email: trimmed, hp }),
        signal: AbortSignal.timeout(8000),
      });

      if (res.ok) {
        setState('ok');
        setMessage('Thanks! We’ll let you know when we launch. No spam.');
      } else {
        let msg = '';
        if (res.status === 429) {
          msg = 'You’re sending requests too fast. Please try again shortly.';
        } else {
          msg = 'That didn’t go through. Please try again.';
        }
        setState('error');
        setMessage(msg);
      }
    } catch {
      setState('error');
      setMessage('Could not reach the server. Please check your connection and try again.');
    }
  };

  if (state === 'ok') {
    return (
      <div className="panel flex flex-col gap-2 px-4 py-3">
        <p className="m-0 text-sm" role="status" aria-live="polite">
          {message}
        </p>
      </div>
    );
  }

  return (
    <form onSubmit={handleSubmit} className="flex flex-wrap gap-2 items-end">
      <label htmlFor="waitlist-email" className="sr-only">
        Email address
      </label>
      <input
        id="waitlist-email"
        type="email"
        value={email}
        onChange={(e) => setEmail(e.target.value)}
        placeholder="you@example.com"
        required
        maxLength={254}
        autoComplete="email"
        className="flex-1 min-w-[150px] rounded border border-[var(--border)] bg-[var(--panel)] text-[var(--text)] px-3 py-2"
        aria-invalid={state === 'error'}
        aria-describedby={state === 'error' ? 'waitlist-status' : undefined}
      />
      <input
        name="hp"
        type="text"
        className="hidden"
        tabIndex={-1}
        autoComplete="off"
        aria-hidden="true"
      />
      <button
        type="submit"
        disabled={state === 'sending'}
        className="chip"
        style={{ borderColor: 'var(--accent)', color: 'var(--accent)' }}
      >
        {state === 'sending' ? 'Sending...' : 'Notify me'}
      </button>
      {message !== '' && (
        <p
          id="waitlist-status"
          role="status"
          aria-live="polite"
          className="basis-full mt-1 text-sm"
          style={{ color: state === 'error' ? 'var(--bad)' : 'var(--dim)' }}
        >
          {message}
        </p>
      )}
    </form>
  );
}