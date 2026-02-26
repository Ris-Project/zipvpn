<!DOCTYPE html>
<html lang="id">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Riswan Jabar Store - Logo</title>
    <script src="https://cdn.tailwindcss.com"></script>
    <link href="https://fonts.googleapis.com/css2?family=Bebas+Neue&family=Plus+Jakarta+Sans:wght@400;500&display=swap" rel="stylesheet">
    <style>
        :root {
            --bg: #0f0f0f;
            --fg: #fafafa;
            --accent: #d4a932;
        }

        body {
            font-family: 'Plus Jakarta Sans', sans-serif;
            background: var(--bg);
            color: var(--fg);
            min-height: 100vh;
            display: flex;
            flex-direction: column;
            align-items: center;
            justify-content: center;
            padding: 2rem;
        }

        .font-display {
            font-family: 'Bebas Neue', sans-serif;
        }

        /* Logo */
        .logo-container {
            text-align: center;
        }

        .logo-icon {
            width: 100px;
            height: 100px;
            margin: 0 auto 1.5rem;
        }

        .logo-icon svg {
            filter: drop-shadow(0 0 30px rgba(212, 169, 50, 0.3));
        }

        .ring-outer {
            fill: none;
            stroke: var(--accent);
            stroke-width: 2;
            stroke-dasharray: 345;
            stroke-dashoffset: 345;
            animation: draw 1.5s ease-out forwards;
        }

        .ring-inner {
            fill: none;
            stroke: var(--accent);
            stroke-width: 1;
            opacity: 0.5;
            stroke-dasharray: 260;
            stroke-dashoffset: 260;
            animation: draw 1.5s ease-out 0.2s forwards;
        }

        .letter-r {
            fill: var(--accent);
            opacity: 0;
            animation: fade 0.6s ease-out 0.8s forwards;
        }

        .brand-name {
            font-size: clamp(2.5rem, 10vw, 4rem);
            letter-spacing: 0.2em;
            color: var(--fg);
            opacity: 0;
            animation: fade 0.6s ease-out 1s forwards;
        }

        .brand-sub {
            font-size: clamp(0.75rem, 2vw, 1rem);
            letter-spacing: 0.4em;
            color: var(--accent);
            opacity: 0;
            animation: fade 0.6s ease-out 1.2s forwards;
        }

        .divider {
            width: 50px;
            height: 1px;
            background: var(--accent);
            margin: 1rem auto;
            opacity: 0;
            animation: fade 0.6s ease-out 1.1s forwards;
        }

        @keyframes draw {
            to { stroke-dashoffset: 0; }
        }

        @keyframes fade {
            to { opacity: 1; }
        }

        /* Variations */
        .variations {
            display: flex;
            gap: 2rem;
            margin-top: 4rem;
            flex-wrap: wrap;
            justify-content: center;
        }

        .var-card {
            padding: 2rem 3rem;
            border-radius: 8px;
            text-align: center;
            opacity: 0;
            animation: fade 0.6s ease-out 1.4s forwards;
        }

        .var-dark {
            background: #1a1a1a;
            border: 1px solid #2a2a2a;
        }

        .var-light {
            background: #fafafa;
        }

        .var-card svg {
            width: 50px;
            height: 50px;
            margin-bottom: 0.75rem;
        }

        .var-card .name {
            font-family: 'Bebas Neue', sans-serif;
            font-size: 1.25rem;
            letter-spacing: 0.1em;
        }

        .var-dark .name { color: var(--fg); }
        .var-light .name { color: #1a1a1a; }

        .var-card .sub {
            font-size: 0.65rem;
            letter-spacing: 0.2em;
            opacity: 0.6;
        }

        .var-dark .sub { color: var(--fg); }
        .var-light .sub { color: #1a1a1a; }

        .label {
            font-size: 0.7rem;
            letter-spacing: 0.15em;
            color: #555;
            margin-top: 0.75rem;
            text-transform: uppercase;
        }

        @media (prefers-reduced-motion: reduce) {
            *, *::before, *::after {
                animation-duration: 0.01ms !important;
            }
        }
    </style>
</head>
<body>

    <div class="logo-container">
        <!-- Logo Icon -->
        <div class="logo-icon">
            <svg viewBox="0 0 100 100">
                <circle class="ring-outer" cx="50" cy="50" r="45"/>
                <circle class="ring-inner" cx="50" cy="50" r="35"/>
                <path class="letter-r" d="M38 68V32h15c7 0 12 4 12 11 0 4-2 7-6 9l8 16h-7l-7-13h-8v13h-7zm7-18h8c3 0 5-2 5-5s-2-5-5-5h-8v10z"/>
            </svg>
        </div>

        <div class="divider"></div>

        <!-- Text -->
        <h1 class="brand-name font-display">RISWAN</h1>
        <p class="brand-sub">JABAR STORE</p>
    </div>

    <!-- Variations -->
    <div class="variations">
        <div class="var-card var-dark">
            <svg viewBox="0 0 100 100">
                <circle cx="50" cy="50" r="45" fill="none" stroke="#d4a932" stroke-width="2"/>
                <path d="M38 68V32h15c7 0 12 4 12 11 0 4-2 7-6 9l8 16h-7l-7-13h-8v13h-7zm7-18h8c3 0 5-2 5-5s-2-5-5-5h-8v10z" fill="#d4a932"/>
            </svg>
            <p class="name">RISWAN</p>
            <p class="sub">JABAR STORE</p>
            <p class="label">Dark</p>
        </div>

        <div class="var-card var-light">
            <svg viewBox="0 0 100 100">
                <circle cx="50" cy="50" r="45" fill="none" stroke="#1a1a1a" stroke-width="2"/>
                <path d="M38 68V32h15c7 0 12 4 12 11 0 4-2 7-6 9l8 16h-7l-7-13h-8v13h-7zm7-18h8c3 0 5-2 5-5s-2-5-5-5h-8v10z" fill="#1a1a1a"/>
            </svg>
            <p class="name">RISWAN</p>
            <p class="sub">JABAR STORE</p>
            <p class="label">Light</p>
        </div>
    </div>

</body>
</html>