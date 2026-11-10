// PK95 ProxBackup web UI: single page app (vanilla JS), design of PK95 Core.
"use strict";

const ICONS = {
"home": "M2.25 12l8.954-8.955c.44-.439 1.152-.439 1.591 0L21.75 12M4.5 9.75v10.125c0 .621.504 1.125 1.125 1.125H9.75v-4.875c0-.621.504-1.125 1.125-1.125h2.250c.621 0 1.125.504 1.125 1.125V21h4.125c.621 0 1.125-.504 1.125-1.125V9.75M8.25 21h8.25",
"apps": "M3.75 6A2.25 2.25 0 016 3.75h2.25A2.25 2.25 0 0110.5 6v2.25a2.25 2.25 0 01-2.25 2.25H6a2.25 2.25 0 01-2.25-2.25V6zM3.75 15.75A2.25 2.25 0 016 13.5h2.25a2.25 2.25 0 012.25 2.25V18a2.25 2.25 0 01-2.25 2.25H6A2.25 2.25 0 013.75 18v-2.25zM13.5 6a2.25 2.25 0 012.25-2.25H18A2.25 2.25 0 0120.25 6v2.25A2.25 2.25 0 0118 10.5h-2.25a2.25 2.25 0 01-2.25-2.25V6zM13.5 15.75a2.25 2.25 0 012.25-2.25H18a2.25 2.25 0 012.25 2.25V18A2.25 2.25 0 0118 20.25h-2.25A2.25 2.25 0 0113.5 18v-2.25z",
"server": "M21.75 17.25v-.228a4.5 4.5 0 00-.12-1.03l-2.268-9.64a3.375 3.375 0 00-3.285-2.602H7.923a3.375 3.375 0 00-3.285 2.602l-2.268 9.64a4.5 4.5 0 00-.12 1.03v.228m19.5 0a3 3 0 01-3 3H5.25a3 3 0 01-3-3m19.5 0a3 3 0 00-3-3H5.25a3 3 0 00-3 3m16.5 0h.008v.008h-.008v-.008zm-3 0h.008v.008h-.008v-.008z",
"rocket": "M15.59 14.37a6 6 0 01-5.84 7.38v-4.8m5.84-2.58a14.98 14.98 0 006.16-12.12A14.98 14.98 0 009.631 8.41m5.96 5.96a14.926 14.926 0 01-5.841 2.58m-.119-8.54a6 6 0 00-7.381 5.84h4.8m2.581-5.84a14.927 14.927 0 00-2.58 5.84m2.699 2.7c-.103.021-.207.041-.311.06a15.09 15.09 0 01-2.448-2.448 14.9 14.9 0 01.06-.312m-2.24 2.39a4.493 4.493 0 00-1.757 4.306 4.493 4.493 0 004.306-1.758M16.5 9a1.5 1.5 0 11-3 0 1.5 1.5 0 013 0z",
"cog": "M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.324.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 011.37.49l1.296 2.247a1.125 1.125 0 01-.26 1.431l-1.003.827c-.293.24-.438.613-.431.992a6.759 6.759 0 010 .255c-.007.378.138.75.43.99l1.005.828c.424.35.534.954.26 1.43l-1.298 2.247a1.125 1.125 0 01-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.57 6.57 0 01-.22.128c-.331.183-.581.495-.644.869l-.213 1.28c-.09.543-.56.941-1.11.941h-2.594c-.55 0-1.02-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 01-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 01-1.369-.49l-1.297-2.247a1.125 1.125 0 01.26-1.431l1.004-.827c.292-.24.437-.613.43-.992a6.932 6.932 0 010-.255c.007-.378-.138-.75-.43-.99l-1.004-.828a1.125 1.125 0 01-.26-1.43l1.297-2.247a1.125 1.125 0 011.37-.491l1.216.456c.356.133.751.072 1.076-.124.072-.044.146-.087.22-.128.332-.183.582-.495.644-.869l.214-1.281z M15 12a3 3 0 11-6 0 3 3 0 016 0z",
"plus": "M12 4.5v15m7.5-7.5h-15",
"pencil": "M16.862 4.487l1.687-1.688a1.875 1.875 0 112.652 2.652L10.582 16.07a4.5 4.5 0 01-1.897 1.13L6 18l.8-2.685a4.5 4.5 0 011.13-1.897l8.932-8.931zm0 0L19.5 7.125M18 14v4.75A2.25 2.25 0 0115.75 21H5.25A2.25 2.25 0 013 18.75V8.25A2.25 2.25 0 015.25 6H10",
"trash": "M14.74 9l-.346 9m-4.788 0L9.26 9m9.968-3.21c.342.052.682.107 1.022.166m-1.022-.165L18.16 19.673a2.25 2.25 0 01-2.244 2.077H8.084a2.25 2.25 0 01-2.244-2.077L4.772 5.79m14.456 0a48.108 48.108 0 00-3.478-.397m-12 .562c.34-.059.68-.114 1.022-.165m0 0a48.11 48.11 0 013.478-.397m7.5 0v-.916c0-1.18-.91-2.164-2.09-2.201a51.964 51.964 0 00-3.32 0c-1.18.037-2.09 1.022-2.09 2.201v.916m7.5 0a48.667 48.667 0 00-7.5 0",
"play": "M5.25 5.653c0-.856.917-1.398 1.667-.986l11.54 6.348a1.125 1.125 0 010 1.971l-11.54 6.347a1.125 1.125 0 01-1.667-.985V5.653z",
"stop": "M5.25 7.5A2.25 2.25 0 017.5 5.25h9a2.25 2.25 0 012.25 2.25v9a2.25 2.25 0 01-2.25 2.25h-9a2.25 2.25 0 01-2.25-2.25v-9z",
"power": "M5.636 5.636a9 9 0 1012.728 0M12 3v9",
"refresh": "M16.023 9.348h4.992v-.001M2.985 19.644v-4.992m0 0h4.992m-4.993 0l3.181 3.183a8.25 8.25 0 0013.803-3.7M4.031 9.865a8.25 8.25 0 0113.803-3.7l3.181 3.182m0-4.991v4.99",
"external": "M13.5 6H5.25A2.25 2.25 0 003 8.25v10.5A2.25 2.25 0 005.25 21h10.5A2.25 2.25 0 0018 18.75V10.5m-10.5 6L21 3m0 0h-5.25M21 3v5.25",
"check": "M4.5 12.75l6 6 9-13.5",
"x": "M6 18L18 6M6 6l12 12",
"clock": "M12 6v6h4.5m4.5 0a9 9 0 11-18 0 9 9 0 0118 0z",
"warn": "M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z",
"key": "M15.75 5.25a3 3 0 013 3m3 0a6 6 0 01-7.029 5.912c-.563-.097-1.159.026-1.563.43L10.5 17.25H8.25v2.25H6v2.25H2.25v-2.818c0-.597.237-1.17.659-1.591l6.499-6.499c.404-.404.527-1 .43-1.563A6 6 0 1121.75 8.25z",
"globe": "M12 21a9.004 9.004 0 008.716-6.747M12 21a9.004 9.004 0 01-8.716-6.747M12 21c2.485 0 4.5-4.03 4.5-9S14.485 3 12 3m0 18c-2.485 0-4.5-4.03-4.5-9S9.515 3 12 3m0 0a8.997 8.997 0 017.843 4.582M12 3a8.997 8.997 0 00-7.843 4.582m15.686 0A11.953 11.953 0 0112 10.5c-2.998 0-5.74-1.1-7.843-2.918m15.686 0A8.959 8.959 0 0121 12c0 .778-.099 1.533-.284 2.253m0 0A17.919 17.919 0 0112 16.5c-3.162 0-6.133-.815-8.716-2.247m0 0A9.015 9.015 0 013 12c0-1.605.42-3.113 1.157-4.418",
"copy": "M15.75 17.25v3.375c0 .621-.504 1.125-1.125 1.125h-9.75a1.125 1.125 0 01-1.125-1.125V7.875c0-.621.504-1.125 1.125-1.125H6.75a9.06 9.06 0 011.5.124m7.5 10.376h3.375c.621 0 1.125-.504 1.125-1.125V11.25c0-4.46-3.243-8.161-7.5-8.876a9.06 9.06 0 00-1.5-.124H9.375c-.621 0-1.125.504-1.125 1.125v3.5m7.5 10.375H9.375a1.125 1.125 0 01-1.125-1.125v-9.25m12 6.625v-1.875a3.375 3.375 0 00-3.375-3.375h-1.5a1.125 1.125 0 01-1.125-1.125v-1.5a3.375 3.375 0 00-3.375-3.375H9.75",
"eye": "M2.036 12.322a1.012 1.012 0 010-.639C3.423 7.51 7.36 4.5 12 4.5c4.638 0 8.573 3.007 9.963 7.178.07.207.07.431 0 .639C20.577 16.49 16.64 19.5 12 19.5c-4.638 0-8.573-3.007-9.963-7.178z M15 12a3 3 0 11-6 0 3 3 0 016 0z",
"eye-off": "M3.98 8.223A10.477 10.477 0 001.934 12C3.226 16.338 7.244 19.5 12 19.5c.993 0 1.953-.138 2.863-.395M6.228 6.228A10.45 10.45 0 0112 4.5c4.756 0 8.773 3.162 10.065 7.498a10.522 10.522 0 01-4.293 5.774M6.228 6.228L3 3m3.228 3.228l3.65 3.65m7.894 7.894L21 21m-3.228-3.228l-3.65-3.65m0 0a3 3 0 10-4.243-4.243m4.242 4.242L9.88 9.88",
"cpu": "M8.25 3v1.5M4.5 8.25H3m18 0h-1.5M4.5 12H3m18 0h-1.5m-15 3.75H3m18 0h-1.5M8.25 19.5V21M12 3v1.5m0 15V21m3.75-18v1.5m0 15V21m-9-1.5h10.5a2.25 2.25 0 002.25-2.25V6.75a2.25 2.25 0 00-2.25-2.25H6.75A2.25 2.25 0 004.5 6.75v10.5a2.25 2.25 0 002.25 2.25zm.75-12h9v9h-9v-9z",
"box": "M21 7.5l-9-5.25L3 7.5m18 0l-9 5.25m9-5.25v9l-9 5.25M3 7.5l9 5.25M3 7.5v9l9 5.25m0-9v9",
"back": "M15 19l-7-7 7-7",
"lock": "M16.5 10.5V6.75a4.5 4.5 0 10-9 0v3.75m-.75 11.25h10.5a2.25 2.25 0 002.25-2.25v-6.75a2.25 2.25 0 00-2.25-2.25H6.75a2.25 2.25 0 00-2.25 2.25v6.75a2.25 2.25 0 002.25 2.25z",
"logout": "M15.75 9V5.25A2.25 2.25 0 0013.5 3h-6a2.25 2.25 0 00-2.25 2.25v13.5A2.25 2.25 0 007.5 21h6a2.25 2.25 0 002.25-2.25V15M12 9l-3 3m0 0l3 3m-3-3h12.75",
"user": "M15.75 6a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0zM4.5 20.25a7.5 7.5 0 0115 0",
"sparkles": "M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 00-3.09 3.09z",
"list": "M8.25 6.75h12M8.25 12h12m-12 5.25h12M3.75 6.75h.007v.008H3.75V6.75zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zM3.75 12h.007v.008H3.75V12zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm-.375 5.25h.007v.008H3.75v-.008zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z",
"link": "M13.19 8.688a4.5 4.5 0 011.242 7.244l-4.5 4.5a4.5 4.5 0 01-6.364-6.364l1.757-1.757m13.35-.622l1.757-1.757a4.5 4.5 0 00-6.364-6.364l-4.5 4.5a4.5 4.5 0 001.242 7.244",
"signal": "M9.348 14.651a3.75 3.75 0 010-5.303m5.304 0a3.75 3.75 0 010 5.303m-7.425 2.122a6.75 6.75 0 010-9.546m9.546 0a6.75 6.75 0 010 9.546M5.106 18.894c-3.808-3.808-3.808-9.98 0-13.789m13.788 0c3.808 3.808 3.808 9.981 0 13.79M12 12h.008v.007H12V12zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z",
"info": "M11.25 11.25l.041-.02a.75.75 0 011.063.852l-.708 2.836a.75.75 0 001.063.853l.041-.021M21 12a9 9 0 11-18 0 9 9 0 0118 0zm-9-3.75h.008v.008H12V8.25z",
"shield": "M9 12.75L11.25 15 15 9.75m-3-7.036A11.959 11.959 0 013.598 6 11.99 11.99 0 003 9.749c0 5.592 3.824 10.29 9 11.623 5.176-1.332 9-6.03 9-11.622 0-1.31-.21-2.571-.598-3.751h-.152c-3.196 0-6.1-1.248-8.25-3.285z",
"chevron-down": "M19.5 8.25l-7.5 7.5-7.5-7.5",
"arrow-right": "M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3",
"terminal": "M6.75 7.5l3 2.25-3 2.25m4.5 0h3m-9 8.25h13.5A2.25 2.25 0 0021 18V6a2.25 2.25 0 00-2.25-2.25H5.25A2.25 2.25 0 003 6v12a2.25 2.25 0 002.25 2.25z",
"database": "M20.25 6.375c0 2.278-3.694 4.125-8.25 4.125S3.75 8.653 3.75 6.375m16.5 0c0-2.278-3.694-4.125-8.25-4.125S3.75 4.097 3.75 6.375m16.5 0v11.25c0 2.278-3.694 4.125-8.25 4.125s-8.25-1.847-8.25-4.125V6.375m16.5 0v3.75m-16.5-3.75v3.75m16.5 0v3.75C20.25 16.153 16.556 18 12 18s-8.25-1.847-8.25-4.125v-3.75m16.5 0c0 2.278-3.694 4.125-8.25 4.125s-8.25-1.847-8.25-4.125",
"archive": "M20.25 7.5l-.625 10.632a2.25 2.25 0 01-2.247 2.118H6.622a2.25 2.25 0 01-2.247-2.118L3.75 7.5M10 11.25h4M3.375 7.5h17.25c.621 0 1.125-.504 1.125-1.125v-1.5c0-.621-.504-1.125-1.125-1.125H3.375c-.621 0-1.125.504-1.125 1.125v1.5c0 .621.504 1.125 1.125 1.125z",
"calendar": "M6.75 3v2.25M17.25 3v2.25M3 18.75V7.5a2.25 2.25 0 012.25-2.25h13.5A2.25 2.25 0 0121 7.5v11.25m-18 0A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75m-18 0v-7.5A2.25 2.25 0 015.25 9h13.5A2.25 2.25 0 0121 11.25v7.5",
"folder": "M2.25 12.75V12A2.25 2.25 0 014.5 9.75h15A2.25 2.25 0 0121.75 12v.75m-8.69-6.44l-2.12-2.12a1.5 1.5 0 00-1.061-.44H4.5A2.25 2.25 0 002.25 6v12a2.25 2.25 0 002.25 2.25h15A2.25 2.25 0 0021.75 18V9a2.25 2.25 0 00-2.25-2.25h-5.379a1.5 1.5 0 01-1.06-.44z",
"sync": "M7.5 21L3 16.5m0 0L7.5 12M3 16.5h13.5m0-13.5L21 7.5m0 0L16.5 12M21 7.5H7.5",
"bell": "M14.857 17.082a23.848 23.848 0 005.454-1.31A8.967 8.967 0 0118 9.75v-.7V9A6 6 0 006 9v.75a8.967 8.967 0 01-2.312 6.022c1.733.64 3.56 1.085 5.455 1.31m5.714 0a24.255 24.255 0 01-5.714 0m5.714 0a3 3 0 11-5.714 0",
"download": "M3 16.5v2.25A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75V16.5M16.5 12L12 16.5m0 0L7.5 12m4.5 4.5V3",
"eject": "M3 16.5v2.25A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75V16.5m-13.5-9L12 3m0 0l4.5 4.5M12 3v13.5",
"restore": "M9 15L3 9m0 0l6-6M3 9h12a6 6 0 010 12h-3",
"menu": "M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5",
"search": "M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z",
"moon": "M21.752 15.002A9.718 9.718 0 0118 15.75c-5.385 0-9.75-4.365-9.75-9.75 0-1.33.266-2.597.748-3.752A9.753 9.753 0 003 11.25C3 16.635 7.365 21 12.75 21a9.753 9.753 0 009.002-5.998z",
"sun": "M12 3v2.25m6.364.386l-1.591 1.591M21 12h-2.25m-.386 6.364l-1.591-1.591M12 18.75V21m-4.773-4.227l-1.591 1.591M5.25 12H3m4.227-4.773L5.636 5.636M15.75 12a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0z",
"usb": "M8.25 3v1.5M4.5 8.25H3m18 0h-1.5M4.5 12H3m18 0h-1.5m-15 3.75H3m18 0h-1.5M8.25 19.5V21M12 3v1.5m0 15V21m3.75-18v1.5m0 15V21m-9-1.5h10.5a2.25 2.25 0 002.25-2.25V6.75a2.25 2.25 0 00-2.25-2.25H6.75A2.25 2.25 0 004.5 6.75v10.5a2.25 2.25 0 002.25 2.25zm.75-12h9v9h-9v-9z",
"disk": "M5.25 14.25h13.5m-13.5 0a3 3 0 01-3-3m3 3a3 3 0 100 6h13.5a3 3 0 100-6m-16.5-3a3 3 0 013-3h13.5a3 3 0 013 3m-19.5 0a4.5 4.5 0 01.9-2.7L5.737 5.1a3.375 3.375 0 012.7-1.35h7.126c1.062 0 2.062.5 2.7 1.35l2.587 3.45a4.5 4.5 0 01.9 2.7m0 0a3 3 0 01-3 3m0 3h.008v.008h-.008v-.008zm0-6h.008v.008h-.008v-.008zm-3 6h.008v.008h-.008v-.008zm0-6h.008v.008h-.008v-.008z"
};

// ---------- helpers ----------
const app = document.getElementById("app");
const esc = (v) => String(v ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
const icon = (name, cls = "") => `<svg class="icon ${cls}" viewBox="0 0 24 24" aria-hidden="true"><path d="${ICONS[name] || ICONS.box}"/></svg>`;
const qs = (sel, root = document) => root.querySelector(sel);
const qsa = (sel, root = document) => [...root.querySelectorAll(sel)];

function fmtBytes(n) {
  if (!n) return "0 B";
  const u = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return (n >= 100 || i === 0 ? n.toFixed(0) : n.toFixed(1)).replace(".", ",") + " " + u[i];
}
function fmtTime(ts, withYear) {
  if (!ts) return "—";
  const d = new Date(ts * 1000);
  const p = (x) => String(x).padStart(2, "0");
  return `${p(d.getDate())}.${p(d.getMonth() + 1)}.${withYear ? d.getFullYear() + " " : ""}${p(d.getHours())}:${p(d.getMinutes())}`;
}
function fmtAgo(ts) {
  if (!ts) return "nie";
  const s = Math.round(Date.now() / 1000 - ts);
  if (s < 60) return "gerade eben";
  if (s < 3600) return `vor ${Math.round(s / 60)} Min.`;
  if (s < 86400) return `vor ${Math.round(s / 3600)} Std.`;
  const d = Math.round(s / 86400);
  return d === 1 ? "gestern" : `vor ${d} Tagen`;
}
function fmtDur(a, b) {
  if (!a || !b) return "";
  const s = b - a;
  if (s < 60) return s + " s";
  if (s < 3600) return Math.round(s / 60) + " Min.";
  return (s / 3600).toFixed(1).replace(".", ",") + " Std.";
}
const fmtSecs = (n) => fmtDur(1, 1 + n) || "0 s";
function pct(used, total) { return total ? Math.round((used * 100) / total) : 0; }
function usageBar(used, total) {
  if (!total) return `<div class="usage"><span class="track"></span><span class="pct">—</span></div>`;
  const p = pct(used, total);
  const cls = p >= 90 ? "bad" : p >= 75 ? "warn" : "";
  return `<div class="usage" title="${fmtBytes(used)} von ${fmtBytes(total)}"><span class="track"><span class="fill ${cls}" style="width:${p}%"></span></span><span class="pct">${p} %</span></div>`;
}
const pill = (text, tone = "", dot = false, pulse = false) => `<span class="pill ${tone} ${pulse ? "pulse" : ""}">${dot ? '<span class="d"></span>' : ""}${esc(text)}</span>`;
function taskPill(t) {
  if (!t.endtime) return pill("Läuft", "blue", true, true);
  if (t.status === "OK") return pill("OK", "green");
  if ((t.status || "").startsWith("WARNINGS")) return pill("Warnung", "amber");
  return pill("Fehler", "rose");
}
const TASK_LABELS = {
  vzdump: "Sicherung", backup: "Sicherung", syncjob: "Kopierjob", verify: "Prüfung", verificationjob: "Prüfjob",
  verify_snapshot: "Prüfung", verify_group: "Prüfung", garbage_collection: "Speicher freigeben", prune: "Aufräumen",
  prunejob: "Aufräumjob", qmrestore: "Wiederherstellung VM", vzrestore: "Wiederherstellung CT", reader: "Lesezugriff",
  "mount-device": "Platte einhängen", "unmount-device": "Platte aushängen", "create-datastore": "Datastore anlegen",
  "delete-datastore": "Datastore löschen", dircreate: "Platte vorbereiten", wipedisk: "Platte löschen",
};
const taskLabel = (t) => TASK_LABELS[t] || t;

// ---------- API ----------
async function api(method, path, body) {
  const opt = { method, headers: { "X-PK95": "1" }, credentials: "same-origin" };
  if (body !== undefined) { opt.headers["Content-Type"] = "application/json"; opt.body = JSON.stringify(body); }
  let res;
  try { res = await fetch(path, opt); } catch (e) { throw Object.assign(new Error("Appliance nicht erreichbar."), { network: true }); }
  let data = {};
  try { data = await res.json(); } catch (e) { /* empty */ }
  if (res.status === 401 && !path.startsWith("/api/login") && !path.startsWith("/api/setup")) {
    state.user = null; render(); throw new Error(data.error || "Bitte anmelden.");
  }
  if (!res.ok) throw Object.assign(new Error(data.error || `Fehler ${res.status}`), { fields: data.fields || null, status: res.status });
  return data;
}
const GET = (p) => api("GET", p);
const POST = (p, b) => api("POST", p, b ?? {});
const PUT = (p, b) => api("PUT", p, b ?? {});
const DEL = (p) => api("DELETE", p);
const enc = encodeURIComponent;

// ---------- toasts ----------
function toast(text, kind = "ok") {
  const box = document.getElementById("toasts");
  const el = document.createElement("div");
  el.className = "toast " + (kind === "error" ? "error" : "");
  el.setAttribute("role", kind === "error" ? "alert" : "status");
  el.innerHTML = `<span class="tic">${icon(kind === "error" ? "warn" : "check")}</span><span class="tt">${esc(text)}</span><button type="button" aria-label="Schließen">${icon("x")}</button>`;
  el.querySelector("button").onclick = () => el.remove();
  box.appendChild(el);
  if (kind !== "error") setTimeout(() => el.remove(), 4000);
}
const toastErr = (e) => toast(e.message || String(e), "error");

// ---------- dialogs ----------
function openDialog(html, cls = "") {
  const d = document.createElement("dialog");
  d.className = cls;
  d.innerHTML = html;
  document.body.appendChild(d);
  d.addEventListener("close", () => setTimeout(() => d.remove(), 0));
  d.addEventListener("click", (e) => { if (e.target === d && !d.dataset.busy) d.close(); });
  d.showModal();
  return d;
}

function confirmDialog({ title, text = "", button = "Löschen", tone = "red", ic }) {
  return new Promise((resolve) => {
    const d = openDialog(`
      <div class="dlg-head"><span class="dlg-ic ${tone === "red" ? "red" : ""}">${icon(ic || (tone === "red" ? "trash" : "refresh"))}</span>
        <div class="grow"><div class="dlg-title">${esc(title)}</div>${text ? `<p class="dlg-text">${text}</p>` : ""}</div></div>
      <div class="dlg-foot" style="margin-top:20px"><button class="btn gray" data-r="0" type="button" autofocus>Abbrechen</button><button class="btn ${tone === "red" ? "red" : ""}" data-r="1" type="button">${esc(button)}</button></div>`);
    let result = false;
    qsa("[data-r]", d).forEach((b) => b.onclick = () => { result = b.dataset.r === "1"; d.close(); });
    d.addEventListener("close", () => resolve(result));
    qs('[data-r="0"]', d).focus();
  });
}

// field(spec) renders one form field. spec: {name, label, type, value, hint, options, required, placeholder, wide, mono}
function field(f) {
  const id = "f-" + f.name;
  const req = f.required ? '<span class="req" aria-hidden="true">*</span>' : "";
  const ph = f.placeholder ? ` placeholder="${esc(f.placeholder)}"` : "";
  const cls = f.mono ? ' class="mono"' : "";
  let input = "";
  switch (f.type) {
    case "select":
      input = `<select id="${id}" name="${f.name}">${(f.options || []).map((o) => {
        const [v, l, dis] = Array.isArray(o) ? o : [o, o];
        return `<option value="${esc(v)}" ${String(v) === String(f.value ?? "") ? "selected" : ""} ${dis ? "disabled" : ""}>${esc(l)}</option>`;
      }).join("")}</select>`;
      break;
    case "checkbox":
      return `<div class="field ${f.wide ? "wide" : ""}" data-field="${f.name}"><label class="check"><input type="checkbox" id="${id}" name="${f.name}" ${f.value ? "checked" : ""}><span class="ct"><span>${esc(f.label)}</span>${f.hint ? `<small>${esc(f.hint)}</small>` : ""}</span></label><span class="err" hidden></span></div>`;
    case "radio":
      return `<div class="field wide" data-field="${f.name}">${f.label ? `<span class="fl">${esc(f.label)}${req}</span>` : ""}<div class="radio-cards">${(f.options || []).map(([v, l, h]) =>
        `<label class="radio-card"><input type="radio" name="${f.name}" value="${esc(v)}" ${String(v) === String(f.value) ? "checked" : ""}><span><b>${esc(l)}</b>${h ? `<small>${esc(h)}</small>` : ""}</span></label>`).join("")}</div><span class="err" hidden></span>${f.hint ? `<span class="hint">${esc(f.hint)}</span>` : ""}</div>`;
    case "textarea":
      input = `<textarea id="${id}" name="${f.name}"${ph}${cls} rows="${f.rows || 3}">${esc(f.value)}</textarea>`;
      break;
    case "html":
      return `<div class="field ${f.wide ? "wide" : ""}" data-field="${f.name || ""}">${f.html}</div>`;
    default:
      input = `<input type="${f.type || "text"}" id="${id}" name="${f.name}" value="${esc(f.value)}"${ph}${cls} ${f.autocomplete ? `autocomplete="${f.autocomplete}"` : 'autocomplete="off"'} ${f.min !== undefined ? `min="${f.min}"` : ""} spellcheck="false">`;
  }
  if (f.button) input = `<div class="inline">${input}<button type="button" class="btn gray" data-fbtn="${f.name}">${esc(f.button)}</button></div>`;
  return `<div class="field ${f.wide ? "wide" : ""}" data-field="${f.name}"><label class="fl" for="${id}">${esc(f.label)}${req}</label>${input}${f.hint ? `<span class="hint">${f.hintHTML ? f.hint : esc(f.hint)}</span>` : ""}<span class="err" hidden></span></div>`;
}

function formValues(root) {
  const v = {};
  qsa("input, select, textarea", root).forEach((el) => {
    if (!el.name) return;
    if (el.type === "checkbox") v[el.name] = el.checked;
    else if (el.type === "radio") { if (el.checked) v[el.name] = el.value; }
    else if (el.type === "number") v[el.name] = el.value === "" ? 0 : Number(el.value);
    else v[el.name] = el.value;
  });
  return v;
}

function showErrors(root, err) {
  qsa(".err", root).forEach((e) => { e.hidden = true; e.textContent = ""; });
  qsa("[aria-invalid]", root).forEach((e) => e.removeAttribute("aria-invalid"));
  const box = qs(".form-error", root);
  if (box) { box.hidden = true; box.textContent = ""; }
  if (!err) return;
  let shown = false;
  if (err.fields) {
    for (const [k, msg] of Object.entries(err.fields)) {
      const f = qs(`[data-field="${k}"]`, root);
      if (!f) continue;
      const e = qs(".err", f);
      e.textContent = msg; e.hidden = false; shown = true;
      const inp = qs("input, select, textarea", f);
      if (inp) inp.setAttribute("aria-invalid", "true");
    }
  }
  if (!shown || !err.fields) { if (box) { box.textContent = err.message; box.hidden = false; } else toastErr(err); }
  else if (box && Object.keys(err.fields).some((k) => !qs(`[data-field="${k}"]`, root))) { box.textContent = Object.values(err.fields).join(" "); box.hidden = false; }
}

// formDialog opens a dialog with fields; submit(values, dialog) may throw (field errors are shown).
function formDialog({ title, text, ic = "pencil", tone = "", fields, submit, button = "Speichern", cls = "wide", extra = "", onMount, extraButtons = "" }) {
  const d = openDialog(`
    <form method="dialog" novalidate>
      <div class="dlg-head"><span class="dlg-ic ${tone}">${icon(ic)}</span><div class="grow"><div class="dlg-title">${esc(title)}</div>${text ? `<p class="dlg-text">${text}</p>` : ""}</div></div>
      <div class="dlg-body"><div class="form-error" hidden></div><div class="form-grid">${fields.map(field).join("")}</div>${extra}</div>
      <div class="dlg-foot">${extraButtons}<span class="spacer"></span><button class="btn gray" type="button" data-cancel>Abbrechen</button><button class="btn ${tone === "red" ? "red" : ""}" type="submit">${esc(button)}</button></div>
    </form>`, cls);
  const form = qs("form", d);
  qs("[data-cancel]", d).onclick = () => d.close();
  form.onsubmit = async (e) => {
    e.preventDefault();
    const btn = qs('button[type="submit"]', d);
    btn.disabled = true; d.dataset.busy = "1";
    const old = btn.innerHTML;
    btn.innerHTML = `<span class="spinner"></span> ${esc(button)}`;
    try {
      showErrors(d, null);
      const r = await submit(formValues(form), d);
      if (r !== false) d.close();
    } catch (err) { showErrors(d, err); }
    finally { btn.disabled = false; btn.innerHTML = old; delete d.dataset.busy; }
  };
  const first = qs("input:not([type=hidden]):not([type=checkbox]):not([type=radio]), select, textarea", d);
  if (first) first.focus();
  if (onMount) onMount(d);
  return d;
}

// Task log viewer: polls until the task has stopped.
function showTask(source, upid, title) {
  kickRunning();
  const d = openDialog(`
    <div class="dlg-head"><span class="dlg-ic">${icon("terminal")}</span><div class="grow"><div class="dlg-title">${esc(title || "Aufgabe")}</div><p class="dlg-text mono small">${esc(upid)}</p></div><span data-st></span></div>
    <div class="dlg-body"><pre class="log" data-log>Lade …</pre></div>
    <div class="dlg-foot"><button class="btn gray" type="button" data-close>Schließen</button></div>`, "xwide");
  qs("[data-close]", d).onclick = () => d.close();
  let open = true;
  d.addEventListener("close", () => { open = false; });
  const poll = async () => {
    if (!open) return;
    try {
      const r = await GET(`/api/tasks/log?source=${enc(source)}&upid=${enc(upid)}`);
      const pre = qs("[data-log]", d);
      const atBottom = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 20;
      pre.innerHTML = (r.lines || []).map((l) => {
        const t = esc(l.t);
        return /error|fail|TASK ERROR/i.test(l.t) ? `<span class="bad">${t}</span>` : /TASK OK/.test(l.t) ? `<span class="ok">${t}</span>` : t;
      }).join("\n") || "(noch keine Ausgabe)";
      if (atBottom) pre.scrollTop = pre.scrollHeight;
      const st = r.status || {};
      qs("[data-st]", d).innerHTML = st.status === "stopped" ? taskPill({ endtime: 1, status: st.exitstatus }) : pill("Läuft", "blue", true, true);
      if (st.status !== "stopped") setTimeout(poll, 1500);
    } catch (e) { qs("[data-log]", d).textContent = e.message; }
  };
  poll();
}

async function run(btn, fn, okText) {
  if (btn) { btn.disabled = true; btn.dataset.html = btn.innerHTML; btn.innerHTML = `<span class="spinner"></span>` + (btn.classList.contains("ib") ? "" : " " + btn.textContent.trim()); }
  try { const r = await fn(); if (okText) toast(okText); kickRunning(); return r; }
  catch (e) { toastErr(e); }
  finally { if (btn && btn.isConnected) { btn.disabled = false; btn.innerHTML = btn.dataset.html; } }
}

// ---------- state, router ----------
const state = { user: null, setup: false, version: "", ov: null, servers: [], clusters: [] };
let pageTimer = null;
let pageToken = 0;

const NAV = [
  { path: "/", label: "Übersicht", icon: "home" },
  { head: "Sichern" },
  { path: "/sicherungen", label: "VMs & Container", icon: "archive" },
  { path: "/wiederherstellen", label: "Wiederherstellen", icon: "restore" },
  { path: "/restoretests", label: "Restore-Tests", icon: "shield" },
  { path: "/jobs", label: "Sicherungsjobs", icon: "calendar" },
  { path: "/aufgaben", label: "Aufgaben", icon: "list" },
  { head: "Speicher" },
  { path: "/datastores", label: "Datastores", icon: "database" },
  { path: "/wechselplatten", label: "Wechselplatten", icon: "disk" },
  { path: "/freigaben", label: "NFS & SMB", icon: "folder" },
  { path: "/kopierjobs", label: "Kopierjobs", icon: "sync" },
  { head: "Verwaltung" },
  { path: "/verbindungen", label: "Verbindungen", icon: "link" },
  { path: "/ereignisse", label: "Ereignisse", icon: "bell" },
  { path: "/einstellungen", label: "Einstellungen", icon: "cog" },
];

function go(path) {
  if (location.pathname !== path) history.pushState({}, "", path);
  document.body.classList.remove("menu-open");
  render();
  window.scrollTo(0, 0);
}
window.addEventListener("popstate", render);
document.addEventListener("click", (e) => {
  const a = e.target.closest("a[data-go]");
  if (a && !e.metaKey && !e.ctrlKey && !e.shiftKey) { e.preventDefault(); go(a.getAttribute("href")); }
});

function setTheme() {
  const root = document.documentElement;
  const dark = root.getAttribute("data-theme") ? root.getAttribute("data-theme") === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
  const next = dark ? "light" : "dark";
  root.setAttribute("data-theme", next);
  try { localStorage.setItem("pk95pb-theme", next); } catch (e) { /* ignore */ }
  qsa("[data-theme-icon]").forEach((el) => el.innerHTML = themeIcon());
}
function themeIcon() {
  const root = document.documentElement;
  const dark = root.getAttribute("data-theme") ? root.getAttribute("data-theme") === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
  return icon(dark ? "sun" : "moon");
}

function shell() {
  const cur = location.pathname;
  const failing = (state.ov?.events || []).length;
  let collapsed = false;
  try { collapsed = localStorage.getItem("pk95pb-menu") === "zu"; } catch (e) { /* ignore */ }
  document.body.classList.toggle("collapsed", collapsed);
  app.innerHTML = `
    <nav class="topbar">
      <div class="left">
        <button class="bar-btn mobile-only" type="button" data-menu aria-label="Seitenleiste öffnen">${icon("menu")}</button>
        <button class="bar-btn desk-only" type="button" data-collapse aria-label="Menü ein- oder ausklappen" title="Menü ein- oder ausklappen">${icon("menu")}</button>
        <a href="/" data-go class="brand"><img src="/logo.svg" alt=""><span>PK95 ProxBackup</span></a>
      </div>
      <div class="right">
        <div class="run-wrap"><button class="bar-btn run-btn" type="button" data-run-toggle aria-expanded="false" aria-haspopup="true" title="Laufende Aufgaben">${icon("refresh")}<span class="run-text" hidden></span><span class="run-count" hidden></span></button>
          <div class="run-panel" data-run-panel hidden></div></div>
        <button class="bar-btn" type="button" data-theme-icon title="Zwischen hellem und dunklem Erscheinungsbild wechseln" aria-label="Erscheinungsbild wechseln">${themeIcon()}</button>
        <span class="user-chip"><span class="avatar">${esc((state.user || "?").slice(0, 2))}</span><span class="uname">${esc(state.user)}</span></span>
        <button class="bar-btn" type="button" data-logout title="Abmelden" aria-label="Abmelden">${icon("logout")}</button>
      </div>
    </nav>
    <div class="scrim" data-scrim></div>
    <aside class="sidebar" aria-label="Seitenleiste">
      <ul class="nav-list">
        ${NAV.map((n) => n.head ? `<li class="nav-head">${esc(n.head)}</li>` :
          `<li><a class="nav-link" href="${n.path}" data-go ${cur === n.path || (n.path !== "/" && cur.startsWith(n.path)) ? 'aria-current="page"' : ""}>${icon(n.icon)}<span>${esc(n.label)}</span></a></li>`).join("")}
      </ul>
      <a href="/einstellungen" data-go class="side-foot" style="text-decoration:none">PK95 ProxBackup v${esc(state.version)}</a>
    </aside>
    <main><div class="wrap" id="page"></div></main>`;
  qs("[data-menu]").onclick = () => document.body.classList.toggle("menu-open");
  qs("[data-scrim]").onclick = () => document.body.classList.remove("menu-open");
  qs("[data-collapse]").onclick = () => {
    const c = !document.body.classList.contains("collapsed");
    document.body.classList.toggle("collapsed", c);
    try { localStorage.setItem("pk95pb-menu", c ? "zu" : "auf"); } catch (e) { /* ignore */ }
  };
  qs("[data-theme-icon]").onclick = setTheme;
  bindRunning();
  qs("[data-logout]").onclick = async () => { await POST("/api/logout").catch(() => {}); state.user = null; render(); };
  void failing;
}

// ---------- running tasks (top bar) ----------
const running = { tasks: [], timer: null, open: false, known: new Map(), first: true };

function fmtElapsed(start) {
  if (!start) return "";
  const s = Math.max(0, Math.round(Date.now() / 1000 - start));
  if (s < 60) return s + " s";
  if (s < 3600) return Math.floor(s / 60) + " Min. " + String(s % 60).padStart(2, "0") + " s";
  return Math.floor(s / 3600) + " Std. " + Math.floor((s % 3600) / 60) + " Min.";
}
const pctText = (p) => (p.percent >= 0 ? Math.floor(p.percent) + " %" : "");

function drawRunning() {
  const btn = qs("[data-run-toggle]");
  if (!btn) return;
  const t = running.tasks;
  btn.classList.toggle("active", t.length > 0);
  const cnt = qs(".run-count", btn), txt = qs(".run-text", btn);
  cnt.hidden = t.length < 2; cnt.textContent = t.length;
  if (t.length) {
    const a = t[0], p = a.progress;
    txt.hidden = false;
    txt.innerHTML = `<span class="run-title">${esc(a.label)}${p.percent >= 0 ? " · " + pctText(p) : ""}</span><span class="run-mini">${p.percent >= 0 ? `<span style="width:${p.percent}%"></span>` : ""}</span>`;
    btn.title = t.length === 1 ? "1 Aufgabe läuft" : `${t.length} Aufgaben laufen`;
  } else { txt.hidden = true; btn.title = "Keine laufenden Aufgaben"; }
  // Live progress inside the disk cards of the rotation page.
  qsa("[data-live]").forEach((box) => {
    const a = t.find((x) => x.label === "Wechselplatte " + box.dataset.live || x.label === "Kopie auf Wechselplatte " + box.dataset.live);
    box.hidden = !a;
    if (!a) return;
    const p = a.progress;
    qs("[data-live-text]", box).textContent = a.source === "app" ? (p.step || "") : ["kopiert", p.step, p.detail].filter(Boolean).join(" · ");
    qs("[data-live-pct]", box).textContent = pctText(p);
    const bar = qs(".pbar", box);
    bar.classList.toggle("indet", !(p.percent >= 0));
    qs("span", bar).style.width = p.percent >= 0 ? p.percent + "%" : "";
  });
  const panel = qs("[data-run-panel]");
  if (!running.open) { panel.hidden = true; return; }
  panel.hidden = false;
  panel.innerHTML = `<div class="run-panel-head"><span>Laufende Aufgaben</span><a class="btn gray sm" href="/aufgaben" data-go>Alle Aufgaben</a></div>` + (t.length ? t.map((a, i) => {
    const p = a.progress;
    const sub = [a.server + (a.node ? " · " + a.node : ""), p.step, p.detail].filter(Boolean).map(esc).join(" · ");
    return `<button type="button" class="run-item" data-ri="${i}" ${a.upid ? "" : "disabled"}><div class="ri-top"><span class="ri-label">${esc(a.label)}${a.id && a.source !== "app" && !a.label.includes("Wechselplatte") ? ` <span class="muted small mono">${esc(a.id)}</span>` : ""}</span><span class="ri-pct">${pctText(p) || fmtElapsed(a.starttime)}</span></div>
      <div class="ri-sub">${sub}${p.percent >= 0 && a.starttime ? " · läuft seit " + fmtElapsed(a.starttime) : ""}</div>
      <div class="pbar ${p.percent >= 0 ? "" : "indet"}"><span style="${p.percent >= 0 ? `width:${p.percent}%` : ""}"></span></div></button>`;
  }).join("") : `<div class="run-empty">Gerade läuft nichts.</div>`);
  qsa("[data-ri]", panel).forEach((b) => b.onclick = () => { const a = running.tasks[+b.dataset.ri]; if (a?.upid) { running.open = false; drawRunning(); showTask(a.source, a.upid, a.label); } });
}

async function pollRunning() {
  clearTimeout(running.timer);
  if (!state.user || !qs("[data-run-toggle]")) return;
  try {
    const r = await GET("/api/running");
    const now = new Map(r.tasks.map((t) => [t.upid || t.label, t]));
    if (!running.first) {
      for (const [k, t] of running.known) if (!now.has(k)) toast(`Fertig: ${t.label}`);
    }
    running.first = false;
    running.known = now;
    running.tasks = r.tasks;
    drawRunning();
  } catch (e) { /* keep last state */ }
  running.timer = setTimeout(pollRunning, running.tasks.length || running.open ? 3000 : 12000);
}
// Pages call this after starting something, so the bar shows it right away.
function kickRunning() { setTimeout(pollRunning, 800); }

function bindRunning() {
  const btn = qs("[data-run-toggle]");
  btn.onclick = (e) => { e.stopPropagation(); running.open = !running.open; btn.setAttribute("aria-expanded", running.open); drawRunning(); if (running.open) pollRunning(); };
  qs("[data-run-panel]").onclick = (e) => e.stopPropagation();
  document.addEventListener("click", () => { if (running.open) { running.open = false; btn.setAttribute("aria-expanded", "false"); drawRunning(); } });
  document.addEventListener("keydown", (e) => { if (e.key === "Escape" && running.open) { running.open = false; drawRunning(); } });
  running.first = true;
  pollRunning();
}

function pageHead(title, sub = "", actions = "", back = null) {
  return `<div class="page-head"><div style="min-width:0">${back ? `<a class="back" href="${back[0]}" data-go>${icon("back")}${esc(back[1])}</a>` : ""}<h1>${title}</h1>${sub ? `<div class="sub">${sub}</div>` : ""}</div>${actions ? `<div class="actions">${actions}</div>` : ""}</div>`;
}
const empty = (title, text = "", action = "", ic = "archive") => `<div class="empty">${icon(ic)}${title ? `<b>${esc(title)}</b>` : ""}${text ? `<p>${text}</p>` : ""}${action}</div>`;
const callout = (tone, title, text, actions = "") => `<div class="callout ${tone}"><div class="ct"><div class="ch">${esc(title)}</div><div class="cb">${text}</div></div>${actions ? `<div class="flex">${actions}</div>` : ""}</div>`;

const PAGES = {};

async function render() {
  clearTimeout(pageTimer);
  pageToken++;
  if (!state.user) {
    try {
      const s = await GET("/api/session");
      state.setup = s.setup; state.version = s.version; state.user = s.user || null;
    } catch (e) {
      app.innerHTML = `<div class="sheet-page"><div class="sheet"><div class="sheet-body">${esc(e.message)}</div></div></div>`;
      return;
    }
  }
  if (!state.user) { document.body.classList.remove("collapsed"); return state.setup ? setupPage() : loginPage(); }
  if (!qs(".topbar")) shell();
  else {
    qsa(".nav-link").forEach((a) => {
      const p = a.getAttribute("href");
      const on = location.pathname === p || (p !== "/" && location.pathname.startsWith(p));
      if (on) a.setAttribute("aria-current", "page"); else a.removeAttribute("aria-current");
    });
  }
  const page = PAGES[location.pathname] || PAGES["/"];
  const el = qs("#page");
  const token = pageToken;
  try { await page(el, token); }
  catch (e) { if (token === pageToken) el.innerHTML = pageHead("Fehler") + callout("red", "Die Seite konnte nicht geladen werden", esc(e.message)); }
}

// refreshLoop re-renders a page part every ms while the page is shown.
function every(ms, token, fn) {
  const tick = async () => {
    if (token !== pageToken) return;
    try { await fn(); } catch (e) { /* keep last view */ }
    if (token === pageToken) pageTimer = setTimeout(tick, ms);
  };
  pageTimer = setTimeout(tick, ms);
}

async function loadConn() {
  const [p, c] = await Promise.all([GET("/api/pbs"), GET("/api/pve")]);
  state.servers = p.servers; state.sshKey = p.ssh_key; state.localPBS = p.local_pbs;
  state.clusters = c.clusters;
}
const serverName = (id) => (state.servers.find((s) => s.id === id) || {}).name || id;

// ---------- login & setup (x-anmeldeblatt) ----------
const NETZ = `<svg class="netz" viewBox="0 0 800 600" preserveAspectRatio="xMidYMid slice" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.2">
  <path d="M470 150h120v90h110M590 240v130h-80M510 370h-60v110M700 240v170h-90"/>
  <rect x="440" y="130" width="40" height="40" rx="2"/><rect x="690" y="220" width="40" height="40" rx="2"/><rect x="430" y="460" width="40" height="40" rx="2"/><rect x="590" y="390" width="40" height="40" rx="2"/>
  <path class="paket" stroke-width="3" d="M470 150h120v90h110"/><path class="paket" stroke-width="3" style="animation-delay:-2s" d="M590 240v130h-80M510 370h-60v110"/></svg>`;
function sheet(kopf, body, foot) {
  return `<div class="sheet-page"><div class="sheet-grid"></div>${NETZ}
    <button class="bar-btn sheet-theme" type="button" data-theme-icon aria-label="Erscheinungsbild wechseln">${themeIcon()}</button>
    <div class="sheet"><div class="sheet-strip"><span>${esc(kopf)}</span><span>v${esc(state.version)}</span></div>
    <div class="sheet-body"><h1 class="sheet-brand"><img src="/logo.svg" alt=""><span>PK95 ProxBackup</span></h1>${body}</div>
    ${foot ? `<div class="sheet-foot">${foot}</div>` : ""}</div></div>`;
}
function loginPage() {
  app.innerHTML = sheet("Anmeldung", `<form novalidate>
      <div class="form-error" hidden></div>
      ${field({ name: "username", label: "Benutzername", autocomplete: "username" })}
      ${field({ name: "password", label: "Passwort", type: "password", autocomplete: "current-password" })}
      <button class="btn block" type="submit">Anmelden</button></form>`, "Backup-Appliance für Proxmox VE und Proxmox Backup Server.");
  qs("[data-theme-icon]").onclick = setTheme;
  const form = qs("form");
  qs("input", form).focus();
  form.onsubmit = async (e) => {
    e.preventDefault();
    try { const r = await POST("/api/login", formValues(form)); state.user = r.user; app.innerHTML = ""; render(); }
    catch (err) { showErrors(form, err); }
  };
}
function setupPage() {
  app.innerHTML = sheet("Ersteinrichtung", `<form novalidate>
      <p class="muted" style="font-size:14px">Willkommen! Den <b>Einrichtungscode</b> zeigt die Konsole der VM (Proxmox → VM → Konsole) und das Installationsskript an.</p>
      <div class="form-error" hidden></div>
      ${field({ name: "code", label: "Einrichtungscode", placeholder: "ABCD-EFGH", mono: true })}
      ${field({ name: "username", label: "Benutzername für dich", value: "admin", autocomplete: "username" })}
      ${field({ name: "password", label: "Passwort", type: "password", hint: "Mindestens 10 Zeichen.", autocomplete: "new-password" })}
      <button class="btn block" type="submit">Konto anlegen und starten</button></form>`, "Danach verbindest du Proxmox VE und den Backup-Server.");
  qs("[data-theme-icon]").onclick = setTheme;
  const form = qs("form");
  qs("input", form).focus();
  form.onsubmit = async (e) => {
    e.preventDefault();
    try { const r = await POST("/api/setup", formValues(form)); state.user = r.user; state.setup = false; app.innerHTML = ""; history.replaceState({}, "", "/verbindungen"); render(); }
    catch (err) { showErrors(form, err); }
  };
}

// ---------- Übersicht ----------
PAGES["/"] = async (el, token) => {
  const draw = async (fresh) => {
    const r = await GET("/api/overview" + (fresh ? "?fresh=1" : ""));
    if (token !== pageToken) return;
    const ov = r.overview;
    state.ov = r;
    const servers = ov.servers || [], clusters = ov.clusters || [], shares = ov.shares || [], rots = ov.rotations || [], tasks = ov.tasks || [];
    const stores = servers.flatMap((s) => (s.datastores || []).map((d) => ({ ...d, server: s.name, serverId: s.id })));
    const guests = clusters.reduce((a, c) => a + (c.guests || 0), 0);
    const notBacked = clusters.reduce((a, c) => a + (c.not_backed_up || 0), 0);
    const day = Date.now() / 1000 - 86400;
    const failed = tasks.filter((t) => t.endtime && t.endtime > day && t.status && t.status !== "OK" && !t.status.startsWith("WARNINGS"));
    const backups24 = tasks.filter((t) => t.type === "vzdump" && t.endtime > day);
    const disksIn = rots.flatMap((x) => x.disks).filter((d) => d.present).length;

    let callouts = "";
    if (!servers.length || !clusters.length) {
      callouts += `<div class="panel"><div class="panel-head"><div class="panel-title">Erste Schritte</div></div><ol class="steps">
        <li><span><b>Proxmox VE verbinden</b> – API-Token anlegen und unter <a href="/verbindungen" data-go>Verbindungen</a> eintragen. ${clusters.length ? pill("erledigt", "green") : ""}</span></li>
        <li><span><b>Backup-Server</b> – der PBS dieser Appliance wird automatisch eingetragen; einen vorhandenen PBS fügst du ebenfalls unter Verbindungen hinzu. ${servers.length ? pill("erledigt", "green") : ""}</span></li>
        <li><span><b>Datastore anlegen</b> – auf einer zweiten virtuellen Platte, einer NFS/SMB-Freigabe oder einer USB-Platte.</span></li>
        <li><span><b>In Proxmox VE eintragen</b> und einen <a href="/jobs" data-go>Sicherungsjob</a> anlegen.</span></li>
        <li><span><b>Wechselplatten</b> einrichten, damit eine Kopie außer Haus liegt.</span></li></ol></div>`;
    }
    for (const x of rots) {
      const ready = x.disks.filter((d) => d.state === "ready");
      if (ready.length) callouts += callout("green", `${x.name}: Platte kann gewechselt werden`, esc(x.hint), `<a class="btn gray sm" href="/wechselplatten" data-go>Ansehen</a>`);
      else if (x.overdue) callouts += callout("yellow", `${x.name}: Kopie überfällig`, `Letzte Kopie auf eine Wechselplatte: ${esc(fmtTime(x.last_copy, true))}. ${esc(x.hint)}`, `<a class="btn gray sm" href="/wechselplatten" data-go>Ansehen</a>`);
    }
    for (const s of servers.filter((s) => !s.online)) callouts += callout("red", `${s.name} nicht erreichbar`, esc(s.error || ""));
    for (const c of clusters.filter((c) => !c.online)) callouts += callout("red", `${c.name} nicht erreichbar`, esc(c.error || ""));
    for (const s of shares.filter((s) => !s.mounted)) callouts += callout("red", `Freigabe ${s.name} ist nicht eingehängt`, esc(s.error || s.source), `<a class="btn gray sm" href="/freigaben" data-go>Freigaben</a>`);

    el.innerHTML = pageHead("Übersicht", `Stand ${fmtTime(ov.built)} · aktualisiert sich alle 30 Sekunden`, `<button class="btn gray" type="button" data-refresh>${icon("refresh")} Aktualisieren</button>`) +
      callouts +
      `<div class="tiles">
        <a class="tile ${servers.some((s) => !s.online) ? "red" : "green"}" href="/verbindungen" data-go><span class="ti">${icon("server")}</span><span class="tx"><span class="tv">${servers.filter((s) => s.online).length}/${servers.length}</span><span class="tl">Backup-Server online</span></span></a>
        <a class="tile" href="/datastores" data-go><span class="ti">${icon("database")}</span><span class="tx"><span class="tv">${stores.length}</span><span class="tl">Datastores · ${fmtBytes(stores.reduce((a, d) => a + (d.used || 0), 0))} belegt</span></span></a>
        <a class="tile ${notBacked ? "amber" : "green"}" href="/sicherungen" data-go><span class="ti">${icon("archive")}</span><span class="tx"><span class="tv">${guests - notBacked}/${guests}</span><span class="tl">VMs & CTs in einem Job</span></span></a>
        <a class="tile" href="/aufgaben" data-go><span class="ti">${icon("check")}</span><span class="tx"><span class="tv">${backups24.length}</span><span class="tl">Sicherungsläufe in 24 Std.</span></span></a>
        <a class="tile ${failed.length ? "red" : "green"}" href="/aufgaben" data-go><span class="ti">${icon("warn")}</span><span class="tx"><span class="tv">${failed.length}</span><span class="tl">Fehler in 24 Std.</span></span></a>
        <a class="tile" href="/wechselplatten" data-go><span class="ti">${icon("disk")}</span><span class="tx"><span class="tv">${disksIn}</span><span class="tl">Wechselplatten angesteckt</span></span></a>
      </div>
      <div class="grid">
        <div class="panel flush"><div class="panel-head"><div class="panel-title">Datastores</div><a class="btn gray sm" href="/datastores" data-go>Alle</a></div>
          ${stores.length ? `<div class="table-wrap"><table class="resp"><thead><tr><th>Datastore</th><th>Belegung</th><th class="num">Frei</th></tr></thead><tbody>
          ${stores.map((d) => `<tr><td data-l="Datastore"><div class="t-main">${esc(d.store)} ${d["mount-status"] === "notmounted" ? pill("ausgehängt") : d["mount-status"] === "mounted" ? pill("USB", "blue") : ""}</div><div class="t-sub">${esc(d.server)}</div></td>
            <td data-l="Belegung" style="min-width:160px">${d.total ? usageBar(d.used, d.total) : `<span class="muted small">${d.error ? esc(d.error) : "nicht eingehängt"}</span>`}</td>
            <td class="num" data-l="Frei">${d.total ? fmtBytes(d.avail) : "—"}</td></tr>`).join("")}</tbody></table></div>` : `<div style="padding:0 16px 16px">${empty("Noch kein Datastore", "", `<a class="btn sm" href="/datastores" data-go>Datastore anlegen</a>`, "database")}</div>`}
        </div>
        <div class="panel flush"><div class="panel-head"><div class="panel-title">Wechselplatten</div><a class="btn gray sm" href="/wechselplatten" data-go>Verwalten</a></div>
          <div class="rows">${rots.length ? rots.map((x) => `<div class="row"><div class="grow"><div class="title"><b>${esc(x.name)}</b> <span class="muted small">${x.rhythm === "weekly" ? "wöchentlich" : "täglich"}</span></div>
            <div class="flex mt">${x.disks.map((d) => `<span class="pill ${d.state === "ready" ? "green" : d.state === "error" ? "rose" : d.state === "running" ? "blue pulse" : d.present ? "blue" : ""}"><span class="d"></span>${esc(d.label)}</span>`).join("")}</div>
            <div class="body">${esc(x.hint)}</div></div><div class="small muted nowrap">Letzte Kopie<br><b class="ink-2">${esc(fmtAgo(x.last_copy))}</b></div></div>`).join("") :
            `<div style="padding:0 16px 16px">${empty("Kein Wechselplatten-Satz", "Zwei oder mehr USB-Platten, die täglich oder wöchentlich getauscht werden: eine liegt immer außer Haus.", `<a class="btn sm" href="/wechselplatten" data-go>Einrichten</a>`, "disk")}</div>`}</div>
        </div>
        <div class="panel flush"><div class="panel-head"><div class="panel-title">Letzte Aufgaben</div><a class="btn gray sm" href="/aufgaben" data-go>Alle</a></div>
          <div class="rows">${tasks.slice(0, 9).map((t) => `<div class="row" style="cursor:pointer" data-task="${esc(t.source)}|${esc(t.upid)}"><span class="when">${fmtTime(t.starttime)}</span><div class="grow"><div class="title">${esc(taskLabel(t.type))} <span class="muted">${esc(t.id || "")}</span></div><div class="t-sub">${esc(t.server)}${t.node ? " · " + esc(t.node) : ""}</div></div>${taskPill(t)}</div>`).join("") || `<div class="row muted">Keine Aufgaben.</div>`}</div>
        </div>
        <div class="panel flush"><div class="panel-head"><div class="panel-title">Ereignisse</div><a class="btn gray sm" href="/ereignisse" data-go>Alle</a></div>
          <div class="rows">${(r.events || []).map((e) => eventRow(e, true)).join("") || `<div class="row muted">Noch keine Ereignisse.</div>`}</div>
        </div>
      </div>`;
    qs("[data-refresh]", el).onclick = (e) => run(e.currentTarget, () => draw(true));
    qsa("[data-task]", el).forEach((row) => row.onclick = () => { const [s, u] = row.dataset.task.split("|"); showTask(s, u, "Aufgabe"); });
  };
  el.innerHTML = pageHead("Übersicht", "Lade …");
  await draw(false);
  every(30000, token, () => draw(false));
};

const SEV = { info: ["Info", "blue"], warning: ["Warnung", "amber"], critical: ["Kritisch", "rose"] };
const STATUS = { ok: ["Erfolgreich", "green"], warning: ["Warnung", "amber"], error: ["Fehler", "rose"] };

// eventRow renders an event; with details (guests, facts, log) they open on click.
function eventRow(ev, compact) {
  const d = ev.details;
  const [l, t] = d && d.status ? STATUS[d.status] : (SEV[ev.severity] || SEV.info);
  const rows = (d && d.rows) || [];
  const counts = { ok: 0, warning: 0, error: 0 };
  rows.forEach((r) => { counts[STATUS[r.status] ? r.status : "ok"]++; });
  const chips = rows.length > 1 ? `<span class="flex" style="margin-top:4px">${Object.entries(counts).filter(([, n]) => n).map(([k, n]) => pill(`${n} ${{ ok: "erfolgreich", warning: "Warnung", error: "fehlgeschlagen" }[k]}`, STATUS[k][1])).join("")}</span>` : "";
  const head = `<span class="when" title="${fmtTime(ev.ts, true)}">${fmtTime(ev.ts)}</span>${pill(l, t)}<div class="grow"><div class="title"><b>${esc(ev.source)}:</b> ${esc(ev.title)}</div>${ev.body ? `<div class="body">${esc(ev.body)}</div>` : ""}${chips}</div>`;
  if (!d || compact || !(rows.length || (d.facts || []).length || (d.log || []).length)) return `<div class="row">${head}</div>`;
  return `<details class="row ev-details"><summary class="flex" style="align-items:flex-start;flex-wrap:nowrap">${head}<span class="ev-open">${icon("chevron-down")}</span></summary>
    <div class="ev-body">
      ${(d.facts || []).length ? `<table class="kv">${d.facts.map((f) => `<tr><td>${esc(f.label)}</td><td>${esc(f.value)}</td></tr>`).join("")}</table>` : ""}
      ${rows.length ? `<div class="ev-sub">${esc(d.rows_title || "Einträge")}</div><div class="table-wrap"><table class="resp ev-table"><thead><tr><th>Status</th><th>Gast</th><th>Dauer</th><th>Daten</th><th>Hinweis</th></tr></thead><tbody>
        ${rows.map((r) => { const [rl, rt] = STATUS[r.status] || STATUS.ok; return `<tr><td data-l="Status">${pill(rl, rt)}</td><td data-l="Gast"><span class="tag mono">${esc(r.kind || "")}</span> <b>${esc(r.id)}</b> ${esc(r.name || "")}</td>
          <td data-l="Dauer" class="nowrap">${esc(r.duration || (r.time ? fmtTime(r.time) : ""))}</td><td data-l="Daten" class="nowrap">${esc(r.size || "")}</td><td data-l="Hinweis" class="${r.status === "ok" ? "muted" : "bad"} small">${esc(r.info || "")}</td></tr>`; }).join("")}
        </tbody></table></div>` : ""}
      ${(d.log || []).length ? `<div class="ev-sub">Auszug aus dem Protokoll</div><pre class="log">${d.log.map(esc).join("\n")}</pre>` : ""}
      ${d.link ? `<div><a class="btn gray sm" href="${esc(d.link)}" data-go>Ansehen</a></div>` : ""}
    </div></details>`;
}

// ---------- Verbindungen ----------
PAGES["/verbindungen"] = async (el, token) => {
  const draw = async () => {
    const [ovr] = await Promise.all([GET("/api/overview"), loadConn()]);
    if (token !== pageToken) return;
    const st = Object.fromEntries((ovr.overview.servers || []).map((s) => [s.id, s]));
    const cst = Object.fromEntries((ovr.overview.clusters || []).map((s) => [s.id, s]));
    el.innerHTML = pageHead("Verbindungen", "Proxmox VE und Proxmox Backup Server, die diese Appliance verwaltet. Zugang jeweils über einen API-Token.") +
      `<div class="panel flush"><div class="panel-head"><div class="panel-title">Backup-Server</div><div class="actions"><button class="btn" type="button" data-add-pbs>${icon("plus")} Backup-Server hinzufügen</button></div></div>
        <p class="panel-intro">Der PBS dieser Appliance trägt sich selbst ein. Einen vorhandenen PBS bindest du mit einem API-Token ein; für Freigaben (NFS/SMB) auf ihm braucht die Appliance zusätzlich SSH.</p>
        ${state.servers.length ? `<div class="table-wrap"><table class="resp"><thead><tr><th>Name</th><th>Adresse</th><th>Status</th><th>Kommandozeile</th><th></th></tr></thead><tbody>
        ${state.servers.map((s) => { const x = st[s.id] || {}; return `<tr>
          <td data-l="Name"><div class="t-main">${esc(s.name)} ${s.local ? pill("Appliance", "blue") : ""}</div><div class="t-sub mono">${esc(s.auth_id)}</div></td>
          <td data-l="Adresse"><span class="mono small">${esc(s.local ? (s.public_host || "—") : s.host + ":" + (s.port || 8007))}</span><div class="t-sub">${esc(s.fingerprint.slice(0, 23))}…</div></td>
          <td data-l="Status">${x.online ? pill("Online", "green", true) + `<div class="t-sub">PBS ${esc(x.version)}</div>` : pill("Offline", "rose") + `<div class="t-sub">${esc(x.error || "")}</div>`}</td>
          <td data-l="Kommandozeile">${s.local ? '<span class="small">lokal</span>' : s.ssh_enabled ? '<span class="small">SSH ' + esc(s.ssh_user || "root") + "@" + esc(s.ssh_host || s.host) + "</span>" : '<span class="small muted">nur API</span>'}</td>
          <td class="act"><div class="row-actions">${!s.local && s.ssh_enabled ? `<button class="btn gray sm" type="button" data-ssh="${s.id}">SSH testen</button>` : ""}<button class="ib" type="button" title="Bearbeiten" aria-label="Bearbeiten" data-edit-pbs="${s.id}">${icon("pencil")}</button>${s.local ? "" : `<button class="ib red" type="button" title="Entfernen" aria-label="Entfernen" data-del-pbs="${s.id}">${icon("trash")}</button>`}</div></td></tr>`; }).join("")}
        </tbody></table></div>` : `<div style="padding:0 16px 16px">${empty("Noch kein Backup-Server", state.localPBS ? "Der lokale PBS wird gerade eingebunden …" : "Auf dieser Maschine ist kein Proxmox Backup Server installiert. Einen vorhandenen PBS hinzufügen.", "", "server")}</div>`}
      </div>
      <div class="panel flush"><div class="panel-head"><div class="panel-title">Proxmox VE</div><div class="actions"><button class="btn" type="button" data-add-pve>${icon("plus")} Proxmox VE hinzufügen</button></div></div>
        <p class="panel-intro">Ein Knoten genügt, auch bei einem Cluster. Darüber legt die Appliance Sicherungsjobs an, startet Sicherungen und stellt wieder her.</p>
        ${state.clusters.length ? `<div class="table-wrap"><table class="resp"><thead><tr><th>Name</th><th>Adresse</th><th>Status</th><th>Gäste</th><th></th></tr></thead><tbody>
        ${state.clusters.map((c) => { const x = cst[c.id] || {}; return `<tr>
          <td data-l="Name"><div class="t-main">${esc(c.name)}</div><div class="t-sub mono">${esc(c.token_id)}</div></td>
          <td data-l="Adresse"><span class="mono small">${esc(c.host)}:${c.port || 8006}</span></td>
          <td data-l="Status">${x.online ? pill("Online", "green", true) + `<div class="t-sub">PVE ${esc(x.version)} · ${x.nodes} Knoten</div>` : pill("Offline", "rose") + `<div class="t-sub">${esc(x.error || "")}</div>`}</td>
          <td data-l="Gäste">${x.online ? ((x.missing || []).length ? '<span class="bad small">keine Rechte</span>' : `${x.guests} ${x.not_backed_up ? `<div class="t-sub bad">${x.not_backed_up} ohne Job</div>` : ""}`) : "—"}</td>
          <td class="act"><div class="row-actions"><button class="btn gray sm" type="button" data-storages="${c.id}">${icon("database")} Backup-Speicher</button><button class="ib" type="button" title="Bearbeiten" aria-label="Bearbeiten" data-edit-pve="${c.id}">${icon("pencil")}</button><button class="ib red" type="button" title="Entfernen" aria-label="Entfernen" data-del-pve="${c.id}">${icon("trash")}</button></div></td></tr>`; }).join("")}
        </tbody></table></div>` : `<div style="padding:0 16px 16px">${empty("Noch kein Proxmox VE verbunden", "Unten steht, wie du den API-Token anlegst.", "", "server")}</div>`}
        ${state.clusters.filter((c) => (cst[c.id]?.missing || []).length).map((c) => `<div style="padding:0 16px 16px">${permCallout(c, cst[c.id].missing)}</div>`).join("")}
      </div>
      <div class="grid">
        <div class="panel"><div class="panel-head"><div class="panel-title">Token für Proxmox VE</div></div>
          <p class="panel-intro">In der Shell eines Proxmox-VE-Knotens (Weboberfläche → Knoten → Shell):</p>
          ${codeBlock("pveum user token add root@pam pk95 --privsep 0")}
          <p class="muted small mt">Zeigt Token-ID (<code>root@pam!pk95</code>) und Secret. Das Secret erscheint nur einmal.</p></div>
        <div class="panel"><div class="panel-head"><div class="panel-title">Token für einen vorhandenen PBS</div></div>
          <p class="panel-intro">In der Shell des Backup-Servers:</p>
          ${codeBlock("proxmox-backup-manager user generate-token root@pam pk95\nproxmox-backup-manager acl update / Admin --auth-id 'root@pam!pk95'")}
          <p class="muted small mt">Für Freigaben am externen PBS zusätzlich den SSH-Schlüssel der Appliance eintragen:</p>
          ${codeBlock(`mkdir -p /root/.ssh && echo '${state.sshKey}' >> /root/.ssh/authorized_keys`)}</div>
      </div>`;
    bindCopy(el);
    qs("[data-add-pbs]", el).onclick = () => pbsForm(null, draw);
    qs("[data-add-pve]", el).onclick = () => pveForm(null, draw);
    qsa("[data-edit-pbs]", el).forEach((b) => b.onclick = () => pbsForm(state.servers.find((s) => s.id === b.dataset.editPbs), draw));
    qsa("[data-edit-pve]", el).forEach((b) => b.onclick = () => pveForm(state.clusters.find((s) => s.id === b.dataset.editPve), draw));
    qsa("[data-ssh]", el).forEach((b) => b.onclick = () => run(b, async () => {
      const r = await POST(`/api/pbs/${b.dataset.ssh}/test-ssh`);
      toast("SSH funktioniert: " + r.output.split("\n")[0] + (r.missing.length ? ` – es fehlt: ${r.missing.join(", ")} (apt install nfs-common cifs-utils)` : ""), r.missing.length ? "error" : "ok");
    }));
    qsa("[data-del-pbs]", el).forEach((b) => b.onclick = async () => {
      const s = state.servers.find((x) => x.id === b.dataset.delPbs);
      if (await confirmDialog({ title: `${s.name} entfernen?`, text: "Nur die Verbindung wird gelöscht. Sicherungen und Einstellungen auf dem Server bleiben unverändert.", button: "Entfernen" }))
        run(null, async () => { await DEL(`/api/pbs/${s.id}`); await draw(); }, "Verbindung entfernt.");
    });
    qsa("[data-del-pve]", el).forEach((b) => b.onclick = async () => {
      const s = state.clusters.find((x) => x.id === b.dataset.delPve);
      if (await confirmDialog({ title: `${s.name} entfernen?`, text: "Nur die Verbindung wird gelöscht. Sicherungsjobs und Speicher in Proxmox VE bleiben unverändert.", button: "Entfernen" }))
        run(null, async () => { await DEL(`/api/pve/${s.id}`); await draw(); }, "Verbindung entfernt.");
    });
    qsa("[data-storages]", el).forEach((b) => b.onclick = () => pveStoragesDialog(state.clusters.find((c) => c.id === b.dataset.storages)));
  };
  el.innerHTML = pageHead("Verbindungen", "Lade …");
  await draw();
};

// permCallout explains a PVE token without the needed rights, with the command that fixes it.
function permCallout(c, missing) {
  const user = c.token_id.split("!")[0];
  const cmds = (user !== "root@pam" ? `pveum acl modify / --users '${user}' --roles Administrator\n` : "") + `pveum acl modify / --tokens '${c.token_id}' --roles Administrator`;
  return `<div class="callout red" style="margin:0"><div class="ct"><div class="ch">${esc(c.name)}: dem API-Token fehlen Rechte</div>
    <div class="cb">Ohne sie sieht die Appliance keine VMs, Container und Sicherungsjobs. Fehlend: ${missing.map(esc).join(", ")}.<br>
    Ein Token mit „Privilege Separation“ hat nie mehr Rechte als sein Benutzer${user !== "root@pam" ? ` – deshalb braucht auch <code>${esc(user)}</code> die Rolle` : ""}. Auf einem Knoten von ${esc(c.name)} in der Shell ausführen:</div>
    <div class="mt">${codeBlock(cmds)}</div>
    <div class="cb mt">Kontrolle: <code>pveum user permissions '${esc(c.token_id)}' --path /</code> – danach diese Seite neu laden.</div></div></div>`;
}

function codeBlock(text) {
  return `<div class="code"><pre>${esc(text)}</pre><button class="ib" type="button" title="Kopieren" aria-label="Kopieren" data-copy="${esc(text)}">${icon("copy")}</button></div>`;
}
function bindCopy(root) {
  qsa("[data-copy]", root).forEach((b) => b.onclick = async () => {
    try { await navigator.clipboard.writeText(b.dataset.copy); toast("Kopiert."); }
    catch (e) { toast("Kopieren nicht möglich – bitte markieren und kopieren.", "error"); }
  });
}

function probeButton(d, hostName, portName, fpName, defPort) {
  const btn = qs(`[data-fbtn="${fpName}"]`, d);
  if (!btn) return;
  btn.onclick = () => run(btn, async () => {
    const v = formValues(qs("form", d));
    showErrors(d, null);
    try {
      const r = await POST("/api/probe", { host: v[hostName], port: Number(v[portName]) || defPort });
      qs(`[name="${fpName}"]`, d).value = r.fingerprint;
      toast("Fingerabdruck abgerufen – bitte mit dem auf dem Server vergleichen.");
    } catch (e) { showErrors(d, e); }
  });
}

function pbsForm(s, done) {
  const edit = !!s;
  s = s || { port: 8007, ssh_user: "root", ssh_port: 22 };
  if (s.local) {
    return formDialog({ title: "Backup-Server dieser Appliance", ic: "server", text: "Läuft in dieser VM und ist mit einem eigenen Token verbunden. Für Proxmox VE zählt die Adresse, unter der die Appliance im Netz erreichbar ist.",
      fields: [{ name: "name", label: "Name", value: s.name, required: true }, { name: "public_host", label: "Adresse für Proxmox VE", value: s.public_host, hint: "IP oder Hostname dieser VM, z. B. 192.168.1.20", mono: true }],
      submit: async (v) => { await PUT(`/api/pbs/${s.id}`, v); toast("Gespeichert."); done(); } });
  }
  formDialog({
    title: edit ? `${s.name} bearbeiten` : "Backup-Server hinzufügen", ic: "server",
    text: "Ein vorhandener Proxmox Backup Server. Token mit der Rolle Admin auf / (siehe unten auf der Seite).",
    fields: [
      { name: "name", label: "Name", value: s.name, required: true, placeholder: "PBS Büro" },
      { name: "host", label: "Adresse", value: s.host, required: true, placeholder: "192.168.1.30", mono: true },
      { name: "port", label: "Port", type: "number", value: s.port || 8007 },
      { name: "auth_id", label: "Token-ID", value: s.auth_id, required: true, placeholder: "root@pam!pk95", mono: true },
      { name: "secret", label: "Secret", type: "password", value: "", required: !edit, placeholder: edit ? "unverändert" : "xxxxxxxx-xxxx-…", mono: true },
      { name: "fingerprint", label: "Fingerabdruck des Zertifikats", value: s.fingerprint, required: true, wide: true, mono: true, button: "Abrufen", hint: "Steht im PBS unter Dashboard → „Show Fingerprint“." },
      { name: "public_host", label: "Adresse für Proxmox VE (optional)", value: s.public_host, placeholder: "wie Adresse", mono: true, hint: "Nur nötig, wenn PVE den Server unter einer anderen Adresse erreicht." },
      { type: "html", name: "sshsep", wide: true, html: `<div class="section-title" style="margin-top:6px">SSH-Zugang (für NFS/SMB-Freigaben auf diesem PBS)</div><div class="hint">Schlüssel der Appliance auf dem PBS eintragen:</div>${codeBlock(`mkdir -p /root/.ssh && echo '${state.sshKey}' >> /root/.ssh/authorized_keys`)}` },
      { name: "ssh_enabled", label: "SSH verwenden", type: "checkbox", value: s.ssh_enabled, hint: "Der Hostschlüssel wird beim ersten Kontakt gemerkt." },
      { name: "ssh_host", label: "SSH-Adresse", value: s.ssh_host, placeholder: "wie Adresse", mono: true },
      { name: "ssh_port", label: "SSH-Port", type: "number", value: s.ssh_port || 22 },
      { name: "ssh_user", label: "SSH-Benutzer", value: s.ssh_user || "root", mono: true },
    ],
    onMount: (d) => { probeButton(d, "host", "port", "fingerprint", 8007); bindCopy(d); },
    submit: async (v) => {
      if (edit) await PUT(`/api/pbs/${s.id}`, v); else await POST("/api/pbs", v);
      toast(edit ? "Gespeichert." : "Backup-Server verbunden.");
      done();
    },
  });
}

function pveForm(s, done) {
  const edit = !!s;
  s = s || { port: 8006 };
  formDialog({
    title: edit ? `${s.name} bearbeiten` : "Proxmox VE hinzufügen", ic: "server",
    text: "Adresse eines Knotens und ein API-Token ohne Privilege Separation (oder mit der Rolle Administrator).",
    fields: [
      { name: "name", label: "Name", value: s.name, required: true, placeholder: "Cluster Zuhause" },
      { name: "host", label: "Adresse eines Knotens", value: s.host, required: true, placeholder: "192.168.1.10", mono: true },
      { name: "port", label: "Port", type: "number", value: s.port || 8006 },
      { name: "token_id", label: "Token-ID", value: s.token_id, required: true, placeholder: "root@pam!pk95", mono: true },
      { name: "secret", label: "Secret", type: "password", required: !edit, placeholder: edit ? "unverändert" : "xxxxxxxx-xxxx-…", mono: true },
      { name: "fingerprint", label: "Fingerabdruck des Zertifikats", value: s.fingerprint, required: true, wide: true, mono: true, button: "Abrufen", hint: "Steht in PVE unter Knoten → System → Zertifikate (pve-ssl.pem)." },
    ],
    onMount: (d) => probeButton(d, "host", "port", "fingerprint", 8006),
    submit: async (v) => {
      if (edit) await PUT(`/api/pve/${s.id}`, v); else await POST("/api/pve", v);
      toast(edit ? "Gespeichert." : "Proxmox VE verbunden.");
      done();
    },
  });
}

async function pveStoragesDialog(cl) {
  const d = openDialog(`<div class="dlg-head"><span class="dlg-ic">${icon("database")}</span><div class="grow"><div class="dlg-title">Backup-Speicher in ${esc(cl.name)}</div>
    <p class="dlg-text">PBS-Datastores, die Proxmox VE als Ziel für Sicherungen kennt. Beim Eintragen legt die Appliance auf dem Backup-Server einen eigenen Token an, der nur diesen Datastore benutzen darf.</p></div></div>
    <div class="dlg-body" data-body><div class="flex"><span class="spinner"></span> Lade …</div></div>
    <div class="dlg-foot"><button class="btn gray" type="button" data-close>Schließen</button></div>`, "xwide");
  qs("[data-close]", d).onclick = () => d.close();
  const body = qs("[data-body]", d);
  const draw = async () => {
    const r = await GET(`/api/pve/${cl.id}/storages`);
    const pbs = r.storages.filter((s) => s.type === "pbs");
    body.innerHTML = `${pbs.length ? `<div class="table-wrap"><table class="resp"><thead><tr><th>Speicher</th><th>Ziel</th><th>Benutzer</th><th></th></tr></thead><tbody>
      ${pbs.map((s) => `<tr><td data-l="Speicher"><div class="t-main">${esc(s.storage)} ${s.disable ? pill("aus") : ""}</div></td><td data-l="Ziel">${esc(s.pbs_name || s.server)} / <b>${esc(s.datastore)}</b>${s.namespace ? ` <span class="tag">ns ${esc(s.namespace)}</span>` : ""}</td><td data-l="Benutzer" class="mono small">${esc(s.username)}</td>
        <td class="act"><button class="ib red" type="button" title="Austragen" aria-label="Austragen" data-del="${esc(s.storage)}">${icon("trash")}</button></td></tr>`).join("")}</tbody></table></div>` : `<div class="empty">${icon("database")}<b>Noch kein PBS-Speicher</b><p>Unten einen Datastore eintragen.</p></div>`}
      <div class="section"><div class="section-title">Datastore eintragen</div><div class="form-error" hidden></div><form class="form-grid" novalidate data-add>
        ${field({ name: "pbs", label: "Backup-Server", type: "select", options: [["", "– wählen –"], ...state.servers.map((s) => [s.id, s.name])] })}
        ${field({ name: "store", label: "Datastore", type: "select", options: [["", "– zuerst Server wählen –"]] })}
        ${field({ name: "name", label: "Name in Proxmox VE", placeholder: "pbs-backup", mono: true })}
        ${field({ name: "namespace", label: "Namespace (optional)", placeholder: "leer = Wurzel", mono: true })}
        <div class="field wide"><div><button class="btn" type="submit">${icon("plus")} Eintragen</button></div></div></form></div>`;
    const form = qs("[data-add]", body);
    const sel = qs('[name="pbs"]', form);
    sel.onchange = async () => {
      const st = qs('[name="store"]', form);
      st.innerHTML = `<option value="">Lade …</option>`;
      if (!sel.value) return;
      try {
        const x = await GET(`/api/pbs/${sel.value}/datastores`);
        st.innerHTML = x.datastores.filter((s) => !s.removable).map((s) => `<option>${esc(s.name)}</option>`).join("") || `<option value="">(kein Datastore)</option>`;
        const nm = qs('[name="name"]', form);
        if (!nm.value) nm.value = "pbs-" + (st.value || "backup");
        st.onchange = () => { nm.value = "pbs-" + st.value; };
      } catch (e) { st.innerHTML = ""; toastErr(e); }
    };
    form.onsubmit = async (e) => {
      e.preventDefault();
      const btn = qs("button[type=submit]", form);
      try { btn.disabled = true; showErrors(body, null); await POST(`/api/pve/${cl.id}/storages`, formValues(form)); toast("In Proxmox VE eingetragen."); await draw(); }
      catch (err) { showErrors(body, err); } finally { btn.disabled = false; }
    };
    qsa("[data-del]", body).forEach((b) => b.onclick = async () => {
      if (await confirmDialog({ title: `Speicher ${b.dataset.del} austragen?`, text: "Proxmox VE kennt den Speicher danach nicht mehr; Sicherungsjobs mit diesem Ziel schlagen fehl. Die Sicherungen auf dem Backup-Server bleiben erhalten.", button: "Austragen" }))
        run(b, async () => { await DEL(`/api/pve/${cl.id}/storages/${enc(b.dataset.del)}`); await draw(); }, "Ausgetragen.");
    });
  };
  try { await draw(); } catch (e) { body.innerHTML = `<div class="form-error">${esc(e.message)}</div>`; }
}

// ---------- server picker ----------
function currentServer() {
  const want = new URLSearchParams(location.search).get("server");
  if (want && state.servers.some((s) => s.id === want)) return want;
  try { const s = localStorage.getItem("pk95pb-server"); if (s && state.servers.some((x) => x.id === s)) return s; } catch (e) { /* ignore */ }
  return state.servers[0]?.id;
}
function serverPicker(cur) {
  if (state.servers.length < 2) return "";
  return `<select data-server aria-label="Backup-Server" style="width:auto;min-width:220px">${state.servers.map((s) => `<option value="${s.id}" ${s.id === cur ? "selected" : ""}>${esc(s.name)}</option>`).join("")}</select>`;
}
function bindPicker(el) {
  const sel = qs("[data-server]", el);
  if (sel) sel.onchange = () => { try { localStorage.setItem("pk95pb-server", sel.value); } catch (e) { /* ignore */ } history.replaceState({}, "", location.pathname + "?server=" + sel.value); render(); };
}
function noServer(el, title) {
  el.innerHTML = pageHead(title) + empty("Kein Backup-Server verbunden", "Unter Verbindungen den PBS dieser Appliance prüfen oder einen vorhandenen PBS hinzufügen.", `<a class="btn" href="/verbindungen" data-go>Verbindungen</a>`, "server");
}

function keepText(j) {
  if (!j) return "";
  const parts = [["keep-last", "letzte"], ["keep-daily", "tägl."], ["keep-weekly", "wöch."], ["keep-monthly", "monatl."], ["keep-yearly", "jährl."]]
    .filter(([k]) => j[k]).map(([k, l]) => `${j[k]} ${l}`);
  return parts.join(" · ");
}

// ---------- Datastores ----------
PAGES["/datastores"] = async (el, token) => {
  await loadConn();
  const sid = currentServer();
  if (!sid) return noServer(el, "Datastores");
  const srv = state.servers.find((s) => s.id === sid);
  const draw = async () => {
    const r = await GET(`/api/pbs/${sid}/datastores`);
    if (token !== pageToken) return;
    const list = r.datastores;
    el.innerHTML = pageHead("Datastores", `Speicherorte der Sicherungen auf <b>${esc(srv.name)}</b>. Aufbewahrung, Prüfung und Speicherfreigabe laufen nach Zeitplan auf dem Backup-Server.`,
      `${serverPicker(sid)}<button class="btn" type="button" data-add>${icon("plus")} Datastore anlegen</button>`) +
      (list.length ? `<div class="panel flush"><div class="table-wrap"><table class="resp"><thead><tr><th>Datastore</th><th style="min-width:180px">Belegung</th><th>Aufbewahrung</th><th>Zeitpläne</th><th></th></tr></thead><tbody>
      ${list.map((d) => {
        const mounted = d["mount-status"] !== "notmounted";
        return `<tr class="${mounted ? "" : "off"}">
        <td data-l="Datastore"><div class="t-main">${esc(d.name)} ${d.removable ? pill(mounted ? "USB eingehängt" : "USB ausgehängt", mounted ? "blue" : "") : ""} ${d.share ? pill("Freigabe", "blue") : ""} ${d["maintenance-mode"] ? pill("Wartung", "amber") : ""}</div>
          <div class="t-sub mono">${esc(d.path)}</div>${d.comment ? `<div class="t-sub">${esc(d.comment)}</div>` : ""}${d.rotation ? `<div class="t-sub">Wechselplatte: ${esc(d.rotation)}</div>` : ""}${d.share ? `<div class="t-sub">auf „${esc(d.share)}“</div>` : ""}</td>
        <td data-l="Belegung">${d.total ? usageBar(d.used, d.total) + `<div class="t-sub">${fmtBytes(d.used)} von ${fmtBytes(d.total)}${d["estimated-full-date"] > 0 ? " · voll ca. " + fmtTime(d["estimated-full-date"], true).slice(0, 10) : ""}</div>` : `<span class="small muted">${esc(d.error || (mounted ? "—" : "Platte nicht eingehängt"))}</span>`}</td>
        <td data-l="Aufbewahrung" class="small">${d.prune ? esc(keepText(d.prune)) + `<div class="t-sub">${esc(d.prune.schedule)}${d.prune["last-run-state"] && d.prune["last-run-state"] !== "OK" ? ' · <span class="bad">' + esc(d.prune["last-run-state"]) + "</span>" : ""}</div>` : d.rotation ? '<span class="muted">über Wechselplatten-Satz</span>' : '<span class="muted">alles behalten</span>'}</td>
        <td data-l="Zeitpläne" class="small"><div>Freigeben: ${esc(d.gc_schedule || "—")}</div><div>Prüfen: ${esc(d.verify?.schedule || "—")}</div></td>
        <td class="act"><div class="row-actions">
          ${d.removable ? `<button class="btn gray sm" type="button" data-mount="${esc(d.name)}" data-on="${mounted ? 0 : 1}">${icon(mounted ? "eject" : "download")} ${mounted ? "Aushängen" : "Einhängen"}</button>` : ""}
          <button class="btn gray sm" type="button" data-snaps="${esc(d.name)}" ${mounted ? "" : "disabled"}>${icon("archive")} Sicherungen</button>
          <button class="ib" type="button" title="Speicher jetzt freigeben (Garbage Collection)" aria-label="Speicher freigeben" data-act="gc" data-store="${esc(d.name)}" ${mounted ? "" : "disabled"}>${icon("sparkles")}</button>
          <button class="ib" type="button" title="Jetzt prüfen (Verify)" aria-label="Prüfen" data-act="verify" data-store="${esc(d.name)}" ${mounted ? "" : "disabled"}>${icon("shield")}</button>
          <button class="ib" type="button" title="Aufbewahrung und Zeitpläne" aria-label="Einstellungen" data-edit="${esc(d.name)}">${icon("cog")}</button>
          <button class="ib red" type="button" title="Entfernen" aria-label="Entfernen" data-del="${esc(d.name)}">${icon("trash")}</button></div></td></tr>`; }).join("")}
      </tbody></table></div></div>` : empty("Noch kein Datastore", "Ein Datastore kann auf einer freien Platte der VM, auf einer NFS/SMB-Freigabe oder in einem Verzeichnis liegen. USB-Wechselplatten richtest du unter „Wechselplatten“ ein.", `<button class="btn" type="button" data-add2>${icon("plus")} Datastore anlegen</button>`, "database")) +
      `<div class="callout"><div class="ct"><div class="ch">So funktioniert das Aufräumen</div><div class="cb">„Aufbewahrung“ entfernt alte Sicherungen nach Regeln (z. B. 7 tägliche, 4 wöchentliche). Den Platz gibt erst „Speicher freigeben“ (Garbage Collection) frei – deshalb läuft beides nach Zeitplan. „Prüfen“ liest die Sicherungen und erkennt beschädigte Daten.</div></div></div>`;
    bindPicker(el);
    const add = () => datastoreCreate(srv, draw);
    qsa("[data-add], [data-add2]", el).forEach((b) => b.onclick = add);
    qsa("[data-act]", el).forEach((b) => b.onclick = () => run(b, async () => {
      const r = await POST(`/api/pbs/${sid}/datastores/${enc(b.dataset.store)}/${b.dataset.act}`);
      showTask("pbs:" + sid, r.upid, (b.dataset.act === "gc" ? "Speicher freigeben: " : "Prüfung: ") + b.dataset.store);
    }));
    qsa("[data-mount]", el).forEach((b) => b.onclick = () => run(b, async () => {
      const r = await POST(`/api/pbs/${sid}/datastores/${enc(b.dataset.mount)}/${b.dataset.on === "1" ? "mount" : "unmount"}`);
      showTask("pbs:" + sid, r.upid, (b.dataset.on === "1" ? "Einhängen: " : "Aushängen: ") + b.dataset.mount);
      setTimeout(draw, 2500);
    }));
    qsa("[data-snaps]", el).forEach((b) => b.onclick = () => snapshotsDialog(srv, b.dataset.snaps));
    qsa("[data-edit]", el).forEach((b) => b.onclick = () => datastoreSettings(srv, list.find((d) => d.name === b.dataset.edit), draw));
    qsa("[data-del]", el).forEach((b) => b.onclick = () => datastoreDelete(srv, b.dataset.del, draw));
  };
  el.innerHTML = pageHead("Datastores", "Lade …");
  await draw();
  every(30000, token, draw);
};

const retentionFields = (v = {}) => [
  { type: "html", wide: true, html: `<div class="section-title">Aufbewahrung</div><div class="hint">Leer oder 0 = diese Regel nicht verwenden. Ganz leer = nichts wird automatisch gelöscht.</div>` },
  { name: "keep_last", label: "Letzte", type: "number", min: 0, value: v["keep-last"] || "" },
  { name: "keep_daily", label: "Täglich", type: "number", min: 0, value: v["keep-daily"] || "" },
  { name: "keep_weekly", label: "Wöchentlich", type: "number", min: 0, value: v["keep-weekly"] || "" },
  { name: "keep_monthly", label: "Monatlich", type: "number", min: 0, value: v["keep-monthly"] || "" },
  { name: "keep_yearly", label: "Jährlich", type: "number", min: 0, value: v["keep-yearly"] || "" },
  { name: "prune_schedule", label: "Aufräumen um", value: v.schedule || "daily", mono: true, hint: "PBS-Zeitplan: daily, 03:00, sat 02:00 …" },
];

function datastoreCreate(srv, done) {
  formDialog({
    title: "Datastore anlegen", ic: "database",
    text: `Auf <b>${esc(srv.name)}</b>.`,
    fields: [
      { name: "kind", type: "radio", label: "Wo sollen die Sicherungen liegen?", value: "disk", options: [
        ["disk", "Freie Platte", "Eine leere (virtuelle) Platte wird formatiert."],
        ["share", "NFS/SMB-Freigabe", "Auf einer unter „NFS & SMB“ eingerichteten Freigabe."],
        ["path", "Verzeichnis", "Ein Pfad auf einem vorhandenen Dateisystem."]] },
      { name: "name", label: "Name", required: true, placeholder: "backup", mono: true, hint: "Buchstaben, Ziffern, - und _" },
      { name: "disk", label: "Platte", type: "select", options: [["", "Lade …"]] },
      { name: "fs", label: "Dateisystem", type: "select", value: "ext4", options: [["ext4", "ext4"], ["xfs", "XFS"]] },
      { name: "share_id", label: "Freigabe", type: "select", options: [["", "Lade …"]] },
      { name: "path", label: "Pfad", placeholder: "/srv/backup", mono: true },
      { name: "gc_schedule", label: "Speicher freigeben um", value: "daily", mono: true, hint: "Garbage Collection, z. B. daily oder sun 06:00" },
      { name: "verify_schedule", label: "Prüfen um", value: "sat 04:00", mono: true, hint: "Leer = nicht automatisch prüfen" },
      ...retentionFields({ "keep-last": 3, "keep-daily": 14, "keep-weekly": 8, "keep-monthly": 12 }),
    ],
    button: "Anlegen",
    onMount: async (d) => {
      const show = () => {
        const k = formValues(qs("form", d)).kind;
        qs('[data-field="disk"]', d).hidden = k !== "disk";
        qs('[data-field="fs"]', d).hidden = k !== "disk";
        qs('[data-field="share_id"]', d).hidden = k !== "share";
        qs('[data-field="path"]', d).hidden = k !== "path";
      };
      qsa('[name="kind"]', d).forEach((r) => r.onchange = show);
      show();
      try {
        const [disks, sh] = await Promise.all([GET(`/api/pbs/${srv.id}/disks`), GET("/api/shares")]);
        const free = disks.disks.filter((x) => x.used === "unused");
        qs('[name="disk"]', d).innerHTML = free.length ? free.map((x) => `<option value="${esc(x.name)}">${esc(x.name)} – ${fmtBytes(x.size)} ${esc(x.vendor || "")} ${esc(x.model || "")} (${esc(x["disk-type"])})</option>`).join("") : `<option value="">Keine freie Platte – der VM in Proxmox eine Platte hinzufügen</option>`;
        const mine = sh.shares.filter((x) => x.server_id === srv.id && !x.store);
        qs('[name="share_id"]', d).innerHTML = mine.length ? mine.map((x) => `<option value="${esc(x.id)}">${esc(x.name)} (${esc(x.source)})${x.mounted ? "" : " – nicht eingehängt"}</option>`).join("") : `<option value="">Keine freie Freigabe – unter „NFS & SMB“ anlegen</option>`;
      } catch (e) { toastErr(e); }
    },
    submit: async (v) => {
      if (v.kind === "disk" && v.disk && !(await confirmDialog({ title: `Platte ${v.disk} formatieren?`, text: "Alle Daten auf der Platte gehen verloren.", button: "Formatieren" }))) return false;
      await POST(`/api/pbs/${srv.id}/datastores`, v);
      toast(`Datastore ${v.name} angelegt.`);
      done();
    },
  });
}

function datastoreSettings(srv, d, done) {
  formDialog({
    title: `${d.name}: Aufbewahrung & Zeitpläne`, ic: "cog",
    fields: [
      { name: "comment", label: "Beschreibung", value: d.comment, wide: true },
      { name: "gc_schedule", label: "Speicher freigeben um", value: d.gc_schedule, mono: true, hint: "Leer = nie automatisch" },
      { name: "verify_schedule", label: "Prüfen um", value: d.verify?.schedule || "", mono: true, hint: "Leer = nie automatisch" },
      ...retentionFields(d.prune || { schedule: "daily" }),
    ],
    submit: async (v) => { await PUT(`/api/pbs/${srv.id}/datastores/${enc(d.name)}`, v); toast("Gespeichert."); done(); },
  });
}

async function datastoreDelete(srv, name, done) {
  const d = openDialog(`<form method="dialog"><div class="dlg-head"><span class="dlg-ic red">${icon("trash")}</span><div class="grow"><div class="dlg-title">Datastore ${esc(name)} entfernen?</div>
    <p class="dlg-text">Standard: Der Backup-Server vergisst den Datastore, die Dateien bleiben liegen und lassen sich später wieder einbinden.</p></div></div>
    <div class="dlg-body"><label class="check"><input type="checkbox" name="destroy"><span class="ct"><span><b>Auch alle Sicherungen darin löschen</b></span><small>Kann nicht rückgängig gemacht werden.</small></span></label>
    <div class="field" data-field="confirm" hidden><label class="fl">Zur Bestätigung den Namen eintippen</label><input type="text" name="confirm" class="mono" autocomplete="off"></div></div>
    <div class="dlg-foot"><button class="btn gray" type="button" data-cancel>Abbrechen</button><button class="btn red" type="submit">Entfernen</button></div></form>`);
  const cb = qs('[name="destroy"]', d);
  cb.onchange = () => { qs('[data-field="confirm"]', d).hidden = !cb.checked; };
  qs("[data-cancel]", d).onclick = () => d.close();
  qs("form", d).onsubmit = async (e) => {
    e.preventDefault();
    if (cb.checked && qs('[name="confirm"]', d).value !== name) { toast("Name stimmt nicht.", "error"); return; }
    d.close();
    await run(null, async () => { await DEL(`/api/pbs/${srv.id}/datastores/${enc(name)}${cb.checked ? "?destroy=1" : ""}`); done(); }, "Datastore entfernt.");
  };
}

async function snapshotsDialog(srv, store) {
  const d = openDialog(`<div class="dlg-head"><span class="dlg-ic">${icon("archive")}</span><div class="grow"><div class="dlg-title">Sicherungen in ${esc(store)}</div><p class="dlg-text">${esc(srv.name)}</p></div>
    <input type="search" placeholder="Filtern: ID, Typ …" data-filter style="max-width:220px"></div>
    <div class="dlg-body" data-body><div class="flex"><span class="spinner"></span> Lade …</div></div>
    <div class="dlg-foot"><span class="muted small" data-count></span><span class="spacer"></span><button class="btn gray" type="button" data-close>Schließen</button></div>`, "xwide");
  qs("[data-close]", d).onclick = () => d.close();
  let snaps = [];
  const body = qs("[data-body]", d);
  const draw = () => {
    const f = qs("[data-filter]", d).value.trim().toLowerCase();
    const list = snaps.filter((s) => !f || `${s["backup-type"]}/${s["backup-id"]} ${s.comment || ""}`.toLowerCase().includes(f));
    qs("[data-count]", d).textContent = `${list.length} von ${snaps.length} Sicherungen`;
    body.innerHTML = list.length ? `<div class="table-wrap" style="max-height:60dvh;overflow:auto"><table class="resp"><thead><tr><th>Gast</th><th>Zeitpunkt</th><th class="num">Größe</th><th>Geprüft</th><th></th></tr></thead><tbody>
      ${list.slice(0, 500).map((s) => `<tr><td data-l="Gast"><span class="tag mono">${esc(s["backup-type"])}</span> <b>${esc(s["backup-id"])}</b>${s.comment ? `<div class="t-sub">${esc(s.comment)}</div>` : ""}</td>
        <td data-l="Zeitpunkt" class="nowrap">${fmtTime(s["backup-time"], true)}</td><td class="num" data-l="Größe">${fmtBytes(s.size)}</td>
        <td data-l="Geprüft">${s.verification ? (s.verification.state === "ok" ? pill("OK", "green") : pill("Fehler", "rose")) : '<span class="muted small">nein</span>'} ${s.protected ? pill("geschützt", "blue") : ""}</td>
        <td class="act"><button class="ib red" type="button" title="Löschen" aria-label="Löschen" data-del="${esc(s["backup-type"])}|${esc(s["backup-id"])}|${s["backup-time"]}" ${s.protected ? "disabled" : ""}>${icon("trash")}</button></td></tr>`).join("")}</tbody></table></div>` : `<div class="empty">${icon("archive")}<b>Keine Sicherungen</b></div>`;
    qsa("[data-del]", body).forEach((b) => b.onclick = async () => {
      const [t, id, time] = b.dataset.del.split("|");
      if (!(await confirmDialog({ title: `Sicherung ${t}/${id} vom ${fmtTime(+time, true)} löschen?`, text: "Der Platz wird beim nächsten „Speicher freigeben“ frei.", button: "Löschen" }))) return;
      await run(b, async () => { await DEL(`/api/pbs/${srv.id}/datastores/${enc(store)}/snapshots?type=${enc(t)}&bid=${enc(id)}&time=${time}`); snaps = snaps.filter((s) => !(s["backup-type"] === t && s["backup-id"] === id && s["backup-time"] === +time)); draw(); }, "Gelöscht.");
    });
  };
  qs("[data-filter]", d).oninput = draw;
  try { snaps = (await GET(`/api/pbs/${srv.id}/datastores/${enc(store)}/snapshots`)).snapshots; draw(); }
  catch (e) { body.innerHTML = `<div class="form-error">${esc(e.message)}</div>`; }
}

// ---------- Kopierjobs ----------
PAGES["/kopierjobs"] = async (el, token) => {
  await loadConn();
  const sid = currentServer();
  if (!sid) return noServer(el, "Kopierjobs");
  const srv = state.servers.find((s) => s.id === sid);
  const draw = async () => {
    const r = await GET(`/api/pbs/${sid}/sync`);
    if (token !== pageToken) return;
    const remotes = Object.fromEntries((r.remotes || []).map((x) => [x.name, x]));
    const jobs = r.jobs || [];
    el.innerHTML = pageHead("Kopierjobs", `Kopien zwischen Datastores, auch von einem anderen Backup-Server (z. B. Außer-Haus-Kopie). Ziel ist <b>${esc(srv.name)}</b>; die Kopie wird dorthin geholt.`,
      `${serverPicker(sid)}<button class="btn" type="button" data-add>${icon("plus")} Kopierjob anlegen</button>`) +
      (jobs.length ? `<div class="panel flush"><div class="table-wrap"><table class="resp"><thead><tr><th>Job</th><th>Von</th><th>Nach</th><th>Zeitplan</th><th>Letzter Lauf</th><th></th></tr></thead><tbody>
      ${jobs.map((j) => { const rot = j.id.startsWith("pk95r-"); const rem = j.remote ? remotes[j.remote] : null; return `<tr>
        <td data-l="Job"><div class="t-main">${esc(j.comment || j.id)}</div><div class="t-sub mono">${esc(j.id)}</div>${rot ? pill("Wechselplatte", "blue") : ""}</td>
        <td data-l="Von">${j.remote ? `<span class="small">${esc(rem ? (rem.comment || "").replace("PK95 ProxBackup: ", "") || rem.host : j.remote)}</span> / ` : ""}<b>${esc(j["remote-store"])}</b></td>
        <td data-l="Nach"><b>${esc(j.store)}</b>${j["transfer-last"] ? `<div class="t-sub">${esc(transferText(j["transfer-last"]))}</div>` : ""}</td>
        <td data-l="Zeitplan" class="small">${esc(j.schedule || (j["run-on-mount"] ? "beim Anstecken" : "von Hand"))}${j["next-run"] ? `<div class="t-sub">nächster: ${fmtTime(j["next-run"])}</div>` : ""}</td>
        <td data-l="Letzter Lauf">${j["last-run-upid"] && !j["last-run-endtime"] ? pill("Läuft", "blue", true, true) : j["last-run-endtime"] ? `${taskPill({ endtime: 1, status: j["last-run-state"] })}<div class="t-sub">${fmtTime(j["last-run-endtime"])}</div>` : '<span class="muted small">nie</span>'}</td>
        <td class="act"><div class="row-actions">${j["last-run-upid"] ? `<button class="ib gray" type="button" title="Protokoll" aria-label="Protokoll" data-log="${esc(j["last-run-upid"])}">${icon("terminal")}</button>` : ""}
          <button class="ib" type="button" title="Jetzt ausführen" aria-label="Jetzt ausführen" data-run="${esc(j.id)}">${icon("play")}</button>
          ${rot ? "" : `<button class="ib" type="button" title="Bearbeiten" aria-label="Bearbeiten" data-edit="${esc(j.id)}">${icon("pencil")}</button><button class="ib red" type="button" title="Löschen" aria-label="Löschen" data-del="${esc(j.id)}">${icon("trash")}</button>`}</div></td></tr>`; }).join("")}
      </tbody></table></div></div>` : empty("Noch kein Kopierjob", "Zum Beispiel: Diese Appliance holt jede Nacht eine Kopie vom PBS im Büro – oder umgekehrt.", `<button class="btn" type="button" data-add2>${icon("plus")} Kopierjob anlegen</button>`, "sync"));
    bindPicker(el);
    qsa("[data-add], [data-add2]", el).forEach((b) => b.onclick = () => syncForm(srv, null, draw));
    qsa("[data-edit]", el).forEach((b) => b.onclick = () => syncForm(srv, jobs.find((j) => j.id === b.dataset.edit), draw));
    qsa("[data-run]", el).forEach((b) => b.onclick = () => run(b, async () => { await POST(`/api/pbs/${sid}/sync/${enc(b.dataset.run)}/run`); setTimeout(draw, 1500); }, "Kopierjob gestartet."));
    qsa("[data-log]", el).forEach((b) => b.onclick = () => showTask("pbs:" + sid, b.dataset.log, "Kopierjob"));
    qsa("[data-del]", el).forEach((b) => b.onclick = async () => {
      if (await confirmDialog({ title: `Kopierjob ${b.dataset.del} löschen?`, text: "Bereits kopierte Sicherungen bleiben erhalten.", button: "Löschen" }))
        run(b, async () => { await DEL(`/api/pbs/${sid}/sync/${enc(b.dataset.del)}`); await draw(); }, "Gelöscht.");
    });
  };
  el.innerHTML = pageHead("Kopierjobs", "Lade …");
  await draw();
  every(15000, token, draw);
};

async function storesOf(serverId) {
  const r = await GET(`/api/pbs/${serverId}/datastores`);
  return r.datastores;
}

function syncForm(srv, job, done) {
  const edit = !!job;
  let srcServer = srv.id;
  if (job?.remote) {
    const m = state.servers.find((s) => "pk95-" + s.id === job.remote);
    if (m) srcServer = m.id;
  }
  formDialog({
    title: edit ? "Kopierjob bearbeiten" : "Kopierjob anlegen", ic: "sync",
    text: `Ziel-Server: <b>${esc(srv.name)}</b>. Liegt die Quelle auf einem anderen Backup-Server, richtet die Appliance dort den Zugang („Remote“) ein.`,
    fields: [
      { name: "source_server", label: "Quelle: Backup-Server", type: "select", value: srcServer, options: state.servers.map((s) => [s.id, s.name]) },
      { name: "source_store", label: "Quelle: Datastore", type: "select", options: [["", "Lade …"]] },
      { name: "store", label: "Ziel-Datastore", type: "select", options: [["", "Lade …"]] },
      { name: "schedule", label: "Zeitplan", value: job?.schedule ?? "daily", mono: true, hint: "z. B. daily, 03:00, mon..fri 23:00 – leer = nur von Hand" },
      { name: "transfer_last", label: "Was kopieren?", type: "select", value: job?.["transfer-last"] || 0, options: TRANSFER_OPTIONS, hint: "Pro VM bzw. Container." },
      { name: "comment", label: "Beschreibung", value: job?.comment || "", wide: true },
      { name: "remove_vanished", label: "Am Ziel löschen, was an der Quelle gelöscht wurde", type: "checkbox", value: job?.["remove-vanished"], wide: true, hint: "Aus: Das Ziel behält auch ältere Sicherungen (eigene Aufbewahrung am Ziel einstellen)." },
    ],
    onMount: async (d) => {
      const fill = async (sel, sid, value) => {
        sel.innerHTML = `<option value="">Lade …</option>`;
        try {
          const list = await storesOf(sid);
          sel.innerHTML = list.map((s) => `<option ${s.name === value ? "selected" : ""}>${esc(s.name)}</option>`).join("");
        } catch (e) { sel.innerHTML = ""; toastErr(e); }
      };
      const ss = qs('[name="source_server"]', d);
      ss.onchange = () => fill(qs('[name="source_store"]', d), ss.value, "");
      await Promise.all([fill(qs('[name="source_store"]', d), srcServer, job?.["remote-store"]), fill(qs('[name="store"]', d), srv.id, job?.store)]);
    },
    submit: async (v) => {
      v.transfer_last = Number(v.transfer_last);
      if (edit) await PUT(`/api/pbs/${srv.id}/sync/${enc(job.id)}`, v); else await POST(`/api/pbs/${srv.id}/sync`, v);
      toast("Gespeichert."); done();
    },
  });
}

// ---------- NFS & SMB ----------
PAGES["/freigaben"] = async (el, token) => {
  await loadConn();
  const draw = async () => {
    const r = await GET("/api/shares");
    if (token !== pageToken) return;
    const list = r.shares;
    el.innerHTML = pageHead("NFS & SMB", "Netzwerkfreigaben (z. B. ein NAS), die auf einem Backup-Server eingehängt werden. Darauf legst du einen Datastore an – die Sicherungen landen dann auf dem NAS.",
      `<button class="btn" type="button" data-add>${icon("plus")} Freigabe hinzufügen</button>`) +
      (list.length ? `<div class="panel flush"><div class="table-wrap"><table class="resp"><thead><tr><th>Freigabe</th><th>Eingehängt auf</th><th>Status</th><th style="min-width:160px">Belegung</th><th>Datastore</th><th></th></tr></thead><tbody>
      ${list.map((s) => `<tr><td data-l="Freigabe"><div class="t-main">${esc(s.name)} <span class="tag">${s.type === "cifs" ? "SMB" : "NFS"}</span></div><div class="t-sub mono">${esc(s.source)}</div></td>
        <td data-l="Eingehängt auf"><div class="small">${esc(s.server_name || s.server_id)}</div><div class="t-sub mono">/mnt/pk95/${esc(s.id)}</div></td>
        <td data-l="Status">${s.mounted ? pill("Eingehängt", "green", true) : pill("Nicht eingehängt", "rose")}${s.error ? `<div class="t-sub bad">${esc(s.error)}</div>` : ""}</td>
        <td data-l="Belegung">${s.mounted ? usageBar(s.used, s.total) + `<div class="t-sub">${fmtBytes(s.avail)} frei</div>` : "—"}</td>
        <td data-l="Datastore">${s.store ? `<a href="/datastores?server=${esc(s.server_id)}" data-go><b>${esc(s.store)}</b></a>` : `<button class="btn gray sm" type="button" data-store="${esc(s.server_id)}">${icon("plus")} Datastore anlegen</button>`}</td>
        <td class="act"><div class="row-actions">
          <button class="btn gray sm" type="button" data-mount="${esc(s.id)}" data-on="${s.mounted ? 0 : 1}">${s.mounted ? "Aushängen" : "Einhängen"}</button>
          <button class="ib" type="button" title="Bearbeiten" aria-label="Bearbeiten" data-edit="${esc(s.id)}">${icon("pencil")}</button>
          <button class="ib red" type="button" title="Entfernen" aria-label="Entfernen" data-del="${esc(s.id)}">${icon("trash")}</button></div></td></tr>`).join("")}
      </tbody></table></div></div>` : empty("Noch keine Freigabe", "Zum Beispiel ein Ordner auf einer Synology oder QNAP. Für den PBS in dieser Appliance klappt es sofort; für einen externen PBS braucht die Appliance dort SSH-Zugang.", `<button class="btn" type="button" data-add2>${icon("plus")} Freigabe hinzufügen</button>`, "folder")) +
      `<div class="grid"><div class="panel"><div class="panel-head"><div class="panel-title">NFS auf dem NAS</div></div><p class="muted" style="font-size:14px">Freigabe für die IP des Backup-Servers mit Lese-/Schreibrecht. PBS schreibt als Benutzer <code>backup</code> (UID 34): bei Synology „Keine Zuordnung“ bzw. <code>no_root_squash</code> wählen, sonst kann der Datastore nicht angelegt werden.</p></div>
       <div class="panel"><div class="panel-head"><div class="panel-title">SMB auf dem NAS</div></div><p class="muted" style="font-size:14px">Ein eigener Benutzer mit Schreibrecht auf den Ordner. Eingehängt wird mit SMB 3.0 und UID 34 (<code>backup</code>). NFS ist für PBS meist schneller und robuster als SMB.</p></div></div>`;
    qsa("[data-add], [data-add2]", el).forEach((b) => b.onclick = () => shareForm(null, draw));
    qsa("[data-edit]", el).forEach((b) => b.onclick = () => shareForm(list.find((s) => s.id === b.dataset.edit), draw));
    qsa("[data-store]", el).forEach((b) => b.onclick = () => datastoreCreate(state.servers.find((s) => s.id === b.dataset.store), () => go("/datastores?server=" + b.dataset.store)));
    qsa("[data-mount]", el).forEach((b) => b.onclick = () => run(b, async () => { await POST(`/api/shares/${b.dataset.mount}/${b.dataset.on === "1" ? "mount" : "unmount"}`); await draw(); }, b.dataset.on === "1" ? "Eingehängt." : "Ausgehängt."));
    qsa("[data-del]", el).forEach((b) => b.onclick = async () => {
      const s = list.find((x) => x.id === b.dataset.del);
      if (await confirmDialog({ title: `Freigabe ${s.name} entfernen?`, text: "Die Freigabe wird ausgehängt und nicht mehr beim Start eingebunden. Die Daten auf dem NAS bleiben erhalten.", button: "Entfernen" }))
        run(b, async () => { await DEL(`/api/shares/${s.id}`); await draw(); }, "Entfernt.");
    });
  };
  el.innerHTML = pageHead("NFS & SMB", "Lade …");
  await draw();
  every(30000, token, draw);
};

function shareForm(s, done) {
  const edit = !!s;
  s = s || { type: "nfs", server_id: state.servers.find((x) => x.local)?.id || state.servers[0]?.id };
  formDialog({
    title: edit ? `${s.name} bearbeiten` : "Freigabe hinzufügen", ic: "folder",
    fields: [
      { name: "type", type: "radio", label: "Art", value: s.type, options: [["nfs", "NFS", "Linux/NAS-Standard, empfohlen"], ["cifs", "SMB / CIFS", "Windows-Freigabe, NAS"]] },
      { name: "name", label: "Name", value: s.name, required: true, placeholder: "Synology NAS" },
      { name: "id", label: "Kennung", value: s.id, required: true, mono: true, placeholder: "nas", hint: "Kleinbuchstaben, Ziffern, _ – wird zu /mnt/pk95/<kennung>" },
      { name: "server_id", label: "Einhängen auf", type: "select", value: s.server_id, options: state.servers.map((x) => [x.id, x.name + (x.local || x.ssh_enabled ? "" : " (kein SSH)"), !(x.local || x.ssh_enabled)]) },
      { name: "source", label: "Quelle", value: s.source, required: true, mono: true, wide: true, placeholder: "192.168.1.5:/volume1/pbs" },
      { name: "username", label: "Benutzer", value: s.username, autocomplete: "off" },
      { name: "password", label: "Passwort", type: "password", placeholder: edit ? "unverändert" : "" },
      { name: "domain", label: "Domäne (optional)", value: s.domain },
      { name: "options", label: "Zusätzliche Optionen", value: s.options, mono: true, placeholder: "z. B. vers=4.1 oder vers=3.1.1", hint: "Werden an die Mount-Optionen angehängt." },
    ],
    extraButtons: `<button class="btn gray" type="button" data-test>${icon("check")} Verbindung testen</button>`,
    onMount: (d) => {
      const form = qs("form", d);
      if (edit) { qs('[name="id"]', d).readOnly = true; qs('[name="server_id"]', d).disabled = true; }
      const show = () => {
        const t = formValues(form).type;
        ["username", "password", "domain"].forEach((n) => { qs(`[data-field="${n}"]`, d).hidden = t !== "cifs"; });
        qs('[name="source"]', d).placeholder = t === "cifs" ? "//nas.local/backup" : "192.168.1.5:/volume1/pbs";
      };
      qsa('[name="type"]', d).forEach((r) => r.onchange = show);
      show();
      const nm = qs('[name="name"]', d), idf = qs('[name="id"]', d);
      if (!edit) nm.oninput = () => { idf.value = nm.value.toLowerCase().replace(/[äöü]/g, (c) => ({ ä: "ae", ö: "oe", ü: "ue" })[c]).replace(/[^a-z0-9]+/g, "_").replace(/^_|_$/g, "").slice(0, 32); };
      const tb = qs("[data-test]", d);
      tb.onclick = () => run(tb, async () => {
        showErrors(d, null);
        const v = formValues(form);
        if (edit) { v.id = "t" + Date.now().toString(36); v.password = v.password || ""; v.server_id = s.server_id; }
        try { await POST("/api/shares/test" + (edit ? "?of=" + enc(s.id) : ""), v); toast("Verbindung klappt: eingehängt und beschreibbar."); }
        catch (e) { showErrors(d, e); }
      });
    },
    submit: async (v) => {
      if (edit) { v.server_id = s.server_id; await PUT(`/api/shares/${s.id}`, v); } else await POST("/api/shares", v);
      toast(edit ? "Gespeichert und neu eingehängt." : "Freigabe eingehängt.");
      done();
    },
  });
}

// ---------- Wechselplatten ----------
const DISK_STATE = {
  absent: ["Nicht angesteckt", ""], idle: ["Angesteckt", "blue"], running: ["Kopiert …", "blue"],
  ready: ["Fertig – abziehen", "green"], error: ["Fehler", "rose"],
};
const PHASE = { pruning: "räumt auf", gc: "gibt Speicher frei", unmounting: "hängt aus", mounting: "hängt ein" };

PAGES["/wechselplatten"] = async (el, token) => {
  await loadConn();
  const draw = async () => {
    const r = await GET("/api/rotations");
    if (token !== pageToken) return;
    const rots = r.rotations;
    el.innerHTML = pageHead("Wechselplatten", "USB-Platten, die sich abwechseln: Eine steckt am Backup-Server und bekommt die Kopie, die andere liegt sicher außer Haus. Getauscht wird täglich oder wöchentlich.",
      `<button class="btn gray" type="button" data-usb>${icon("usb")} USB an diese VM durchreichen</button><button class="btn" type="button" data-add>${icon("plus")} Satz anlegen</button>`) +
      (rots.length ? rots.map((x) => `
      <div class="panel">
        <div class="panel-head"><div><div class="panel-title">${esc(x.name)}</div>
          <div class="muted small">${x.rhythm === "weekly" ? `Wechsel wöchentlich am ${["", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag", "Sonntag"][x.swap_day]}` : "Wechsel täglich"}
          · Quelle <b>${esc(x.source_name)}</b> / ${esc(x.source_store)} · Platten an <b>${esc(x.server_name)}</b>
          · ${x.time ? "Kopie täglich um " + esc(x.time) : ""}${x.time && x.run_on_mount ? " und " : ""}${x.run_on_mount ? "beim Anstecken" : ""}
          · kopiert ${transferText(x.transfer_last)} je VM/CT${x.keep_last === 1 && !x.keep_daily && !x.keep_weekly && !x.keep_monthly ? ", auf der Platte bleibt nur die neueste" : ""}</div></div>
          <div class="actions"><button class="btn gray sm" type="button" data-adddisk="${x.id}">${icon("plus")} Platte hinzufügen</button>
            <button class="ib" type="button" title="Bearbeiten" aria-label="Bearbeiten" data-edit="${x.id}">${icon("pencil")}</button>
            <button class="ib red" type="button" title="Satz entfernen" aria-label="Satz entfernen" data-del="${x.id}">${icon("trash")}</button></div></div>
        ${x.error ? callout("red", "Backup-Server nicht erreichbar", esc(x.error)) : ""}
        ${x.overdue ? callout("yellow", "Kopie überfällig", `Letzte erfolgreiche Kopie: ${esc(fmtTime(x.last_copy, true))}.`) : ""}
        <div class="callout ${x.disks.some((d) => d.state === "ready") ? "green" : ""}" style="margin:0 0 16px"><div class="ct"><div class="ch">${esc(x.hint)}</div>
          <div class="cb">Nächster Wechsel: ${esc(x.next_swap || "—")} · Letzte Kopie: ${esc(fmtAgo(x.last_copy))}${x.unmount_on_done ? " · Platten werden nach der Kopie ausgehängt" : ""}</div></div></div>
        ${x.disks.length ? `<div class="rot-disks">${x.disks.map((d) => {
          const [l, t] = DISK_STATE[d.state] || DISK_STATE.absent;
          const cls = d.state === "ready" ? "ready" : d.state === "error" ? "error" : d.present ? "present" : "";
          return `<div class="disk ${cls}"><div class="disk-head"><span class="disk-ic">${icon("disk")}</span><div class="grow"><div class="disk-name">${esc(d.label)}</div><div class="disk-meta mono">${esc(d.store)}</div><div style="margin-top:6px">${pill(d.phase ? PHASE[d.phase] || d.phase : l, t, true, d.state === "running")}</div></div></div>
            ${d.total ? usageBar(d.used, d.total) : ""}
            <div data-live="${esc(d.label)}" hidden><div class="flex small"><span class="grow" data-live-text></span><b class="mono" data-live-pct></b></div><div class="pbar"><span></span></div></div>
            <table class="kv"><tr><td>Letzte Kopie</td><td>${d.last_ok ? fmtTime(d.last_ok, true) + ` <span class="muted">(${fmtAgo(d.last_ok)})</span>` : "noch keine"}</td></tr>
              ${d.last_fail > d.last_ok ? `<tr><td>Fehler</td><td class="bad">${fmtTime(d.last_fail)} ${esc(d.last_error || "")}</td></tr>` : ""}
              ${d.stale ? `<tr><td>Hinweis</td><td class="bad">lange nicht angesteckt</td></tr>` : ""}
              ${d.error ? `<tr><td>Problem</td><td class="bad">${esc(d.error)}</td></tr>` : ""}</table>
            <div class="disk-foot">
              <button class="btn sm" type="button" data-run="${x.id}|${d.key}" ${d.present && !d.running && !d.phase ? "" : "disabled"}>${icon("play")} Jetzt kopieren</button>
              <button class="btn gray sm" type="button" data-eject="${x.id}|${d.key}" ${d.mounted && !d.running ? "" : "disabled"}>${icon("eject")} Aushängen</button>
              <span class="grow"></span>
              <button class="ib red" type="button" title="Aus dem Satz nehmen" aria-label="Aus dem Satz nehmen" data-rmdisk="${x.id}|${d.key}|${esc(d.label)}">${icon("x")}</button></div></div>`; }).join("")}</div>` :
          empty("Noch keine Platte im Satz", "Platte anstecken und „Platte hinzufügen“ wählen. Für einen sinnvollen Wechsel mindestens zwei Platten.", `<button class="btn sm" type="button" data-adddisk="${x.id}">${icon("plus")} Platte hinzufügen</button>`, "disk")}
      </div>`).join("") :
      empty("Noch kein Wechselplatten-Satz", "Ein Satz sind zwei oder mehr USB-Platten (z. B. „Montag“ bis „Freitag“ oder „Woche A“ und „Woche B“). Jede Platte wird ein eigener Wechsel-Datastore; die Appliance kopiert die Sicherungen darauf, räumt auf, hängt die Platte aus und meldet, wann gewechselt werden kann.", `<button class="btn" type="button" data-add2>${icon("plus")} Satz anlegen</button>`, "disk")) +
      `<div class="grid"><div class="panel"><div class="panel-head"><div class="panel-title">So läuft ein Wechsel</div></div><ol class="steps">
        <li><span>Platte anstecken – am Backup-Server oder (über USB-Durchreichung) an dieser VM.</span></li>
        <li><span>Der Backup-Server hängt sie ein, die Appliance startet die Kopie (sofort oder zur eingestellten Uhrzeit).</span></li>
        <li><span>Danach wird auf der Platte nach den Aufbewahrungsregeln aufgeräumt und sie wird ausgehängt.</span></li>
        <li><span>„Fertig – abziehen“ erscheint, optional kommt eine Benachrichtigung. Platte abziehen und außer Haus bringen.</span></li></ol></div>
       <div class="panel"><div class="panel-head"><div class="panel-title">Platte an dieser VM</div></div><p class="muted" style="font-size:14px">Steckt die Platte am Proxmox-Host, reicht die Appliance den <b>USB-Anschluss</b> an diese VM durch (nicht das Gerät). So erscheint jede Platte, die später in diesem Anschluss steckt, automatisch – ideal zum Wechseln. Voraussetzung: Proxmox VE ist unter Verbindungen eingetragen.</p>
        <div class="mt"><button class="btn gray" type="button" data-usb2>${icon("usb")} USB-Anschluss durchreichen</button></div></div></div>`;
    drawRunning();
    qsa("[data-add], [data-add2]", el).forEach((b) => b.onclick = () => rotationForm(null, draw));
    qsa("[data-usb], [data-usb2]", el).forEach((b) => b.onclick = passthroughDialog);
    qsa("[data-edit]", el).forEach((b) => b.onclick = () => rotationForm(rots.find((x) => x.id === b.dataset.edit), draw));
    qsa("[data-adddisk]", el).forEach((b) => b.onclick = () => addDiskForm(rots.find((x) => x.id === b.dataset.adddisk), draw));
    qsa("[data-del]", el).forEach((b) => b.onclick = async () => {
      const x = rots.find((y) => y.id === b.dataset.del);
      if (await confirmDialog({ title: `Satz „${x.name}“ entfernen?`, text: "Die Kopierjobs werden gelöscht. Die Wechsel-Datastores und die Sicherungen auf den Platten bleiben erhalten.", button: "Entfernen" }))
        run(b, async () => { await DEL(`/api/rotations/${x.id}`); await draw(); }, "Entfernt.");
    });
    qsa("[data-run]", el).forEach((b) => b.onclick = () => run(b, async () => { const [r1, k] = b.dataset.run.split("|"); await POST(`/api/rotations/${r1}/disks/${k}/run`); setTimeout(draw, 1500); }, "Kopie gestartet."));
    qsa("[data-eject]", el).forEach((b) => b.onclick = () => run(b, async () => { const [r1, k] = b.dataset.eject.split("|"); await POST(`/api/rotations/${r1}/disks/${k}/eject`); await draw(); }, "Ausgehängt – die Platte kann abgezogen werden."));
    qsa("[data-rmdisk]", el).forEach((b) => b.onclick = async () => {
      const [r1, k, label] = b.dataset.rmdisk.split("|");
      if (await confirmDialog({ title: `${label} aus dem Satz nehmen?`, text: "Der Kopierjob für diese Platte wird gelöscht. Datastore und Daten auf der Platte bleiben erhalten.", button: "Herausnehmen" }))
        run(b, async () => { await DEL(`/api/rotations/${r1}/disks/${k}`); await draw(); }, "Herausgenommen.");
    });
  };
  el.innerHTML = pageHead("Wechselplatten", "Lade …");
  await draw();
  every(10000, token, draw);
};

const TRANSFER_OPTIONS = [[1, "Nur die neueste Sicherung"], [2, "Die neuesten 2"], [3, "Die neuesten 3"], [7, "Die neuesten 7"], [14, "Die neuesten 14"], [0, "Alle Sicherungen"]];
const transferText = (n) => (!n ? "alle Sicherungen" : n === 1 ? "nur die neueste Sicherung" : `die neuesten ${n} Sicherungen`);

function rotationForm(x, done) {
  const edit = !!x;
  const local = state.servers.find((s) => s.local)?.id || state.servers[0]?.id;
  x = x || { rhythm: "daily", swap_day: 5, time: "22:30", run_on_mount: true, unmount_on_done: true, transfer_last: 1, keep_last: 1, keep_weekly: 0, keep_monthly: 0, keep_daily: 0, server_id: local, source_server: local };
  formDialog({
    title: edit ? `${x.name} bearbeiten` : "Wechselplatten-Satz anlegen", ic: "disk",
    fields: [
      { name: "name", label: "Name", value: x.name, required: true, placeholder: "USB täglich", wide: true },
      { name: "server_id", label: "Platten stecken an", type: "select", value: x.server_id, options: state.servers.map((s) => [s.id, s.name]), hint: "Backup-Server, an dem die USB-Platten angesteckt werden." },
      { name: "source_server", label: "Sicherungen kommen von", type: "select", value: x.source_server, options: state.servers.map((s) => [s.id, s.name]) },
      { name: "source_store", label: "Datastore mit den Sicherungen", type: "select", options: [["", "Lade …"]] },
      { name: "rhythm", type: "radio", label: "Wechsel", value: x.rhythm, options: [["daily", "Täglich", "Jeden Tag eine andere Platte (z. B. Mo–Fr)"], ["weekly", "Wöchentlich", "Eine Platte pro Woche"]] },
      { name: "swap_day", label: "Wechseltag", type: "select", value: x.swap_day || 5, options: [[1, "Montag"], [2, "Dienstag"], [3, "Mittwoch"], [4, "Donnerstag"], [5, "Freitag"], [6, "Samstag"], [7, "Sonntag"]] },
      { name: "time", label: "Täglich kopieren um", type: "time", value: x.time, hint: "Leer = nur beim Anstecken kopieren." },
      { name: "run_on_mount", label: "Beim Anstecken sofort kopieren", type: "checkbox", value: x.run_on_mount, wide: true },
      { name: "unmount_on_done", label: "Nach der Kopie aushängen", type: "checkbox", value: x.unmount_on_done, wide: true, hint: "Dann kann die Platte gefahrlos abgezogen werden." },
      { name: "transfer_last", label: "Was auf die Platte kopieren?", type: "select", wide: true, value: x.transfer_last ?? 0, options: TRANSFER_OPTIONS,
        hint: "Pro VM bzw. Container. „Nur die neueste“ spart Zeit und Platz: ältere Stände bleiben nur auf dem Backup-Server." },
      { type: "html", wide: true, html: `<div class="section-title">Aufbewahrung auf jeder Platte</div><div class="hint">Was nach der Kopie auf der Platte bleibt. „Letzte = 1“: Die Platte enthält nur die neueste Sicherung, ältere werden nach der Kopie entfernt. 0 = Regel nicht verwenden; alles 0 = nichts löschen.</div>` },
      { name: "keep_last", label: "Letzte", type: "number", min: 0, value: x.keep_last },
      { name: "keep_daily", label: "Täglich", type: "number", min: 0, value: x.keep_daily },
      { name: "keep_weekly", label: "Wöchentlich", type: "number", min: 0, value: x.keep_weekly },
      { name: "keep_monthly", label: "Monatlich", type: "number", min: 0, value: x.keep_monthly },
    ],
    onMount: async (d) => {
      const form = qs("form", d);
      const show = () => { qs('[data-field="swap_day"]', d).hidden = formValues(form).rhythm !== "weekly"; };
      // "only the newest": the disk should then hold just that one, too.
      qs('[name="transfer_last"]', d).addEventListener("change", (e) => {
        if (e.target.value !== "1") return;
        const set = (n, v) => { qs(`[name="${n}"]`, d).value = v; };
        set("keep_last", 1); set("keep_daily", 0); set("keep_weekly", 0); set("keep_monthly", 0);
      });
      qsa('[name="rhythm"]', d).forEach((r) => r.onchange = show);
      show();
      if (edit && x.disks.length) qs('[name="server_id"]', d).disabled = true;
      const ss = qs('[name="source_server"]', d), st = qs('[name="source_store"]', d);
      const fill = async (val) => {
        st.innerHTML = `<option value="">Lade …</option>`;
        try { const list = (await storesOf(ss.value)).filter((s) => !s.removable); st.innerHTML = list.map((s) => `<option ${s.name === val ? "selected" : ""}>${esc(s.name)}</option>`).join("") || `<option value="">(kein Datastore)</option>`; }
        catch (e) { st.innerHTML = ""; toastErr(e); }
      };
      ss.onchange = () => fill("");
      await fill(x.source_store);
    },
    submit: async (v) => {
      v.swap_day = Number(v.swap_day);
      v.transfer_last = Number(v.transfer_last);
      if (edit) { if (x.disks.length) v.server_id = x.server_id; await PUT(`/api/rotations/${x.id}`, v); }
      else await POST("/api/rotations", v);
      toast("Gespeichert."); done();
    },
  });
}

function addDiskForm(x, done) {
  formDialog({
    title: `Platte zu „${x.name}“ hinzufügen`, ic: "disk",
    text: `Die Platte muss an <b>${esc(x.server_name)}</b> stecken. Eine neue Platte wird gelöscht und als Wechsel-Datastore eingerichtet; eine schon vorbereitete wählst du als vorhandenen Datastore.`,
    fields: [
      { name: "mode", type: "radio", value: "new", options: [["new", "Neue Platte vorbereiten", "Wird gelöscht und formatiert"], ["existing", "Vorhandener Wechsel-Datastore", "Schon in PBS eingerichtet"]] },
      { name: "label", label: "Bezeichnung", required: true, placeholder: "Montag / Woche A", hint: "Am besten auch außen auf die Platte schreiben." },
      { name: "store", label: "Datastore-Name", required: true, mono: true, placeholder: "usb-montag" },
      { name: "disk", label: "Platte", type: "select", options: [["", "Lade …"]], wide: true },
      { name: "fs", label: "Dateisystem", type: "select", value: "ext4", options: [["ext4", "ext4"], ["xfs", "XFS"]] },
      { name: "confirm", label: "Zur Bestätigung den Plattennamen eintippen", mono: true, placeholder: "z. B. sdc", hint: "Alle Daten auf der Platte gehen verloren." },
      { name: "existing", label: "Wechsel-Datastore", type: "select", options: [["", "Lade …"]], wide: true },
    ],
    button: "Hinzufügen",
    extraButtons: `<button class="btn gray" type="button" data-reload>${icon("refresh")} Platten neu einlesen</button>`,
    onMount: async (d) => {
      const form = qs("form", d);
      const show = () => {
        const m = formValues(form).mode;
        ["store", "disk", "fs", "confirm"].forEach((n) => { qs(`[data-field="${n}"]`, d).hidden = m !== "new"; });
        qs('[data-field="existing"]', d).hidden = m !== "existing";
      };
      qsa('[name="mode"]', d).forEach((r) => r.onchange = show);
      show();
      const lb = qs('[name="label"]', d), stn = qs('[name="store"]', d);
      lb.oninput = () => { stn.value = ("usb-" + lb.value.toLowerCase().replace(/[äöü]/g, (c) => ({ ä: "ae", ö: "oe", ü: "ue" })[c]).replace(/[^a-z0-9]+/g, "-")).replace(/-+$/, "").slice(0, 32); };
      const load = async () => {
        try {
          const [disks, stores] = await Promise.all([GET(`/api/pbs/${x.server_id}/disks`), storesOf(x.server_id)]);
          const usedStores = new Set(x.disks.map((y) => y.store));
          const cand = disks.disks.filter((k) => !(k.partitions || []).some((p) => p.mounted) && k.name !== "sda");
          qs('[name="disk"]', d).innerHTML = cand.length ? cand.map((k) => `<option value="${esc(k.name)}">${esc(k.name)} – ${fmtBytes(k.size)} ${esc(k.vendor || "")} ${esc(k.model || "")} (${k["disk-type"] === "usb" ? "USB" : esc(k["disk-type"])}${k.used !== "unused" ? ", enthält Daten" : ", leer"})</option>`).join("") : `<option value="">Keine passende Platte gefunden – anstecken und neu einlesen</option>`;
          const rem = stores.filter((s) => s.removable && !usedStores.has(s.name));
          qs('[name="existing"]', d).innerHTML = rem.length ? rem.map((s) => `<option value="${esc(s.name)}">${esc(s.name)}${s.rotation ? " (in " + esc(s.rotation) + ")" : ""}</option>`).join("") : `<option value="">Kein freier Wechsel-Datastore</option>`;
        } catch (e) { toastErr(e); }
      };
      const rb = qs("[data-reload]", d);
      rb.onclick = () => run(rb, load);
      await load();
    },
    submit: async (v) => {
      const body = { label: v.label };
      if (v.mode === "new") Object.assign(body, { store: v.store, disk: v.disk, fs: v.fs, confirm: v.confirm });
      else { body.store = v.existing; }
      await POST(`/api/rotations/${x.id}/disks`, body);
      toast("Platte hinzugefügt."); done();
    },
  });
}

async function passthroughDialog() {
  const d = openDialog(`<div class="dlg-head"><span class="dlg-ic">${icon("usb")}</span><div class="grow"><div class="dlg-title">USB-Anschluss an diese VM durchreichen</div>
    <p class="dlg-text">Durchgereicht wird der Anschluss am Proxmox-Host. Jede Platte, die später dort steckt, erscheint in der Appliance. Nach dem Durchreichen die Platte einmal ab- und wieder anstecken.</p></div></div>
    <div class="dlg-body" data-body><div class="flex"><span class="spinner"></span> Suche diese VM in Proxmox VE …</div></div>
    <div class="dlg-foot"><button class="btn gray" type="button" data-close>Schließen</button></div>`, "wide");
  qs("[data-close]", d).onclick = () => d.close();
  const body = qs("[data-body]", d);
  const draw = async () => {
    const r = await GET("/api/passthrough");
    if (r.error && !r.self) { body.innerHTML = `<div class="form-error">${esc(r.error)}</div>`; return; }
    const self = r.self;
    const used = Object.fromEntries(self.usb.map((u) => { const [k, v] = u.split("="); return [(u.match(/host=([\d.-]+)/) || [])[1], k]; }));
    body.innerHTML = `<table class="kv"><tr><td>Diese VM</td><td><b>${self.vmid}</b> ${esc(self.name)} auf ${esc(self.node)} (${esc(self.cluster_name)})</td></tr>
      <tr><td>Durchgereicht</td><td>${self.usb.length ? self.usb.map((u) => `<span class="tag mono">${esc(u)}</span> <button class="btn ghost sm" type="button" data-rm="${esc(u.split("=")[0])}">entfernen</button>`).join("<br>") : '<span class="muted">nichts</span>'}</td></tr></table>
      ${r.error ? `<div class="form-error">${esc(r.error)}</div>` : `<div class="table-wrap"><table class="resp"><thead><tr><th>Gerät am Host</th><th>Anschluss</th><th>Tempo</th><th></th></tr></thead><tbody>
      ${(r.devices || []).filter((x) => x.class !== 9).map((x) => { const port = `${x.busnum}-${x.usbpath}`; return `<tr><td data-l="Gerät"><div class="t-main">${esc(x.manufacturer || "")} ${esc(x.product || "")}</div><div class="t-sub mono">${esc(x.vendid)}:${esc(x.prodid)}</div></td>
        <td data-l="Anschluss" class="mono">${esc(port)}</td><td data-l="Tempo" class="small">${x.speed >= 5000 ? "USB 3" : x.speed >= 480 ? "USB 2" : "langsam"}</td>
        <td class="act">${used[port] ? pill("durchgereicht", "green") : `<button class="btn sm" type="button" data-port="${esc(port)}">Anschluss durchreichen</button>`}</td></tr>`; }).join("")}</tbody></table></div>`}`;
    qsa("[data-port]", body).forEach((b) => b.onclick = () => run(b, async () => { await POST("/api/passthrough", { port: b.dataset.port }); await draw(); }, "Durchgereicht."));
    qsa("[data-rm]", body).forEach((b) => b.onclick = () => run(b, async () => { await POST("/api/passthrough", { remove: b.dataset.rm }); await draw(); }, "Entfernt."));
  };
  try { await draw(); } catch (e) { body.innerHTML = `<div class="form-error">${esc(e.message)}</div>`; }
}

// ---------- VMs & Container ----------
PAGES["/sicherungen"] = async (el, token) => {
  await loadConn();
  let filter = "", onlyIssues = false;
  let data = null;
  const draw = () => {
    if (token !== pageToken || !data) return;
    const now = Date.now() / 1000;
    const list = data.guests.filter((g) => {
      if (filter && !`${g.vmid} ${g.name} ${g.node} ${g.cluster_name}`.toLowerCase().includes(filter)) return false;
      if (onlyIssues) return !g.in_job || !g.last_backup || now - g.last_backup > 2 * 86400;
      return true;
    });
    qs("[data-list]", el).innerHTML = (data.errors || []).map((e) => {
      const c = state.clusters.find((x) => e.startsWith(x.name + ": "));
      return c && e.includes("fehlen Rechte") ? `<div style="margin:12px">${permCallout(c, (e.match(/\(([^)]*)\)/) || ["", ""])[1].split(", ").filter(Boolean))}</div>` : callout("red", "Nicht alles erreichbar", esc(e));
    }).join("") + (list.length ? `<div class="panel flush"><div class="table-wrap"><table class="resp"><thead><tr><th>Gast</th><th>Knoten</th><th>Letzte Sicherung</th><th>Gesichert auf</th><th></th></tr></thead><tbody>
      ${list.map((g) => { const age = g.last_backup ? now - g.last_backup : 0; return `<tr>
        <td data-l="Gast"><div class="t-main"><span class="tag mono">${g.type === "lxc" ? "CT" : "VM"}</span> ${g.vmid} ${esc(g.name)}</div><div class="t-sub">${g.status === "running" ? '<span class="dot green"></span> läuft' : '<span class="dot"></span> ' + esc(g.status)} ${g.in_job ? "" : ' · <span class="bad">in keinem Sicherungsjob</span>'}</div></td>
        <td data-l="Knoten"><div class="small">${esc(g.node)}</div><div class="t-sub">${esc(g.cluster_name)}</div></td>
        <td data-l="Letzte Sicherung">${g.last_backup ? `<span class="${age > 2 * 86400 ? "bad" : ""}">${fmtTime(g.last_backup, true)}</span><div class="t-sub">${fmtAgo(g.last_backup)}</div>` : '<span class="bad">noch nie</span>'}</td>
        <td data-l="Gesichert auf" class="small">${g.backups.map((b) => `<div>${esc(b.server_name)} / <b>${esc(b.store)}</b> <span class="muted">(${b.count})</span></div>`).join("") || "—"}</td>
        <td class="act"><div class="row-actions"><button class="btn sm" type="button" data-backup="${g.cluster}|${g.vmid}">${icon("download")} Jetzt sichern</button>
          <button class="btn gray sm" type="button" data-restore="${g.cluster}|${g.vmid}" ${g.backups.length ? "" : "disabled"}>${icon("restore")} Wiederherstellen</button></div></td></tr>`; }).join("")}
      </tbody></table></div></div>` : empty(data.guests.length ? "Nichts gefunden" : "Keine VMs oder Container", data.guests.length ? "" : "Zuerst unter Verbindungen Proxmox VE eintragen.", "", "archive"));
    bindCopy(el);
    const find = (v) => { const [c, id] = v.split("|"); return data.guests.find((g) => g.cluster === c && String(g.vmid) === id); };
    qsa("[data-backup]", el).forEach((b) => b.onclick = () => backupNow(find(b.dataset.backup)));
    qsa("[data-restore]", el).forEach((b) => b.onclick = () => { const g = find(b.dataset.restore); go(`/wiederherstellen?cluster=${enc(g.cluster)}&vmid=${g.vmid}`); });
  };
  el.innerHTML = pageHead("VMs & Container", "Alle Gäste der verbundenen Proxmox-VE-Systeme mit ihren Sicherungen auf den Backup-Servern.",
    `<input type="search" data-q placeholder="Suchen: Name, ID, Knoten …" style="width:240px"><label class="check"><input type="checkbox" data-issues><span>nur Problemfälle</span></label>`) + `<div data-list><div class="panel flex"><span class="spinner"></span> Lade …</div></div>`;
  qs("[data-q]", el).oninput = (e) => { filter = e.target.value.trim().toLowerCase(); draw(); };
  qs("[data-issues]", el).onchange = (e) => { onlyIssues = e.target.checked; draw(); };
  const load = async () => { data = await GET("/api/guests"); draw(); };
  await load();
  every(60000, token, load);
};

async function backupNow(g) {
  let storages = [];
  try { storages = (await GET(`/api/pve/${g.cluster}/nodes/${enc(g.node)}/storages?content=backup`)).storages; } catch (e) { return toastErr(e); }
  const pbsFirst = [...storages].sort((a, b) => (b.type === "pbs") - (a.type === "pbs"));
  formDialog({
    title: `${g.vmid} ${g.name} jetzt sichern`, ic: "download", button: "Sicherung starten",
    fields: [
      { name: "storage", label: "Ziel", type: "select", options: pbsFirst.length ? pbsFirst.map((s) => [s.storage, `${s.storage} (${s.type === "pbs" ? "Backup-Server" : s.type})`]) : [["", "Kein Backup-Speicher – unter Verbindungen eintragen"]] },
      { name: "mode", label: "Modus", type: "select", value: "snapshot", options: [["snapshot", "Snapshot (läuft weiter)"], ["suspend", "Anhalten"], ["stop", "Herunterfahren"]] },
      { name: "notes", label: "Notiz", placeholder: "z. B. vor dem Update", wide: true },
    ],
    submit: async (v) => {
      const r = await POST("/api/guests/backup", { cluster: g.cluster, node: g.node, vmid: g.vmid, storage: v.storage, mode: v.mode, notes: v.notes });
      showTask(r.source, r.upid, `Sicherung ${g.vmid} ${g.name}`);
    },
  });
}

// ---------- Wiederherstellen ----------
const b64 = (str) => btoa(String.fromCharCode(...new TextEncoder().encode(str)));
const unb64 = (str) => { try { return new TextDecoder().decode(Uint8Array.from(atob(str), (c) => c.charCodeAt(0))); } catch (e) { return str; } };

PAGES["/wiederherstellen"] = async (el, token) => {
  await loadConn();
  const q = new URLSearchParams(location.search);
  const srcKey = q.get("src");
  el.innerHTML = pageHead("Wiederherstellen", "Lade …");
  if (srcKey) {
    const [server, store] = srcKey.split("|");
    const sources = (await GET("/api/restore/sources")).sources;
    const src = sources.find((x) => x.server === server && x.store === store);
    if (token !== pageToken) return;
    if (!src) { el.innerHTML = pageHead("Wiederherstellen", "", "", ["/wiederherstellen", "Quelle wählen"]) + callout("red", "Quelle nicht gefunden", esc(store)); return; }
    if (q.get("vmid") && src.mounted) {
      const list = (await GET(`/api/restore/source?pbs=${enc(server)}&store=${enc(store)}`)).guests;
      const sg = list.find((x) => String(x.vmid) === q.get("vmid") && (!q.get("type") || x.type === q.get("type")));
      if (sg && token === pageToken) {
        return restoreGuest(el, token, { ...sg, backups: [{ server, server_name: src.server_name, store, count: sg.count, last: sg.last_backup }] }, src);
      }
    }
    return restoreSourceList(el, token, src);
  }
  const [data, srcs] = await Promise.all([GET("/api/guests"), GET("/api/restore/sources")]);
  if (token !== pageToken) return;
  const guests = data.guests.filter((g) => g.backups.length);
  const g = guests.find((x) => x.cluster === q.get("cluster") && String(x.vmid) === q.get("vmid"));
  if (g) return restoreGuest(el, token, g);
  restoreStart(el, data, guests, srcs);
};

const SRC_ICON = { datastore: "database", share: "folder", usb: "disk" };
const SRC_KIND = { datastore: "Datastore", share: "NAS-Freigabe", usb: "USB-Platte" };
const srcHref = (x) => `/wiederherstellen?src=${enc(x.server + "|" + x.store)}`;
function srcState(x) {
  if (x.mounted) return pill("Bereit", "green", true);
  if (x.kind === "usb" && x.present) return pill("Angesteckt, nicht eingehängt", "amber");
  if (x.kind === "usb") return pill("Nicht angesteckt");
  return pill("Nicht verfügbar", "rose");
}

async function mountSource(btn, x, done) {
  await run(btn, async () => {
    const r = await POST(`/api/pbs/${x.server}/datastores/${enc(x.store)}/mount`);
    if (r.upid) {
      for (let i = 0; i < 60; i++) {
        await new Promise((res) => setTimeout(res, 1000));
        const st = await GET(`/api/tasks/log?source=${enc("pbs:" + x.server)}&upid=${enc(r.upid)}`).catch(() => null);
        if (st && st.status && st.status.status === "stopped") {
          if (st.status.exitstatus !== "OK") throw new Error("Einhängen fehlgeschlagen: " + st.status.exitstatus);
          break;
        }
      }
    }
  }, `„${x.label}“ ist eingehängt.`);
  done();
}

// Step 1: where from? All backups, or one datastore / NAS share / USB disk.
function restoreStart(el, data, guests, srcs) {
  const sources = srcs.sources || [];
  el.innerHTML = pageHead("Wiederherstellen", "Einzelne Dateien und Ordner aus einer Sicherung holen oder eine ganze VM bzw. einen Container zurückspielen.") +
    (data.errors || []).concat(srcs.errors || []).map((e) => callout("red", "Nicht alles erreichbar", esc(e))).join("") +
    `<div class="panel"><div class="panel-head"><div class="panel-title">1. Quelle wählen</div></div>
      <div class="src-cards">
        <div class="src-card on"><div class="flex" style="flex-wrap:nowrap">${icon("archive")}<div class="grow"><b>Alle Sicherungen</b><div class="t-sub">alle eingehängten Datastores</div></div></div>${pill("Ausgewählt", "blue")}</div>
        ${sources.map((x, i) => `<div class="src-card ${x.kind === "usb" && !x.present ? "off" : ""}">
          <div class="flex" style="flex-wrap:nowrap">${icon(SRC_ICON[x.kind])}<div class="grow"><b>${esc(x.label)}</b><div class="t-sub">${SRC_KIND[x.kind]} · ${esc(x.server_name)}${x.label !== x.store ? " · " + esc(x.store) : ""}</div></div></div>
          <div class="flex">${srcState(x)}</div>
          ${x.total ? usageBar(x.used, x.total) : ""}
          <div class="flex">${x.mounted ? `<a class="btn sm" href="${srcHref(x)}" data-go>Sicherungen ansehen</a>` : x.kind === "usb" && x.present ? `<button class="btn sm" type="button" data-mount="${i}">${icon("download")} Einhängen</button>` : x.kind === "usb" ? `<span class="small muted">Platte anstecken, dann neu laden</span>` : `<span class="small bad">${esc(x.error || "")}</span>`}</div>
        </div>`).join("")}
      </div></div><div data-list></div>`;
  qsa("[data-mount]", el).forEach((b) => b.onclick = () => { const x = sources[+b.dataset.mount]; mountSource(b, x, () => go(srcHref(x))); });
  const list = qs("[data-list]", el);
  list.innerHTML = `<div class="panel flush"><div class="panel-head"><div class="panel-title">2. Server wählen</div><input type="search" data-q placeholder="Suchen: Name, ID …" style="width:240px"></div><div data-rows></div></div>`;
  const draw = (f) => {
    const rows = guests.filter((x) => !f || `${x.vmid} ${x.name} ${x.node} ${x.cluster_name}`.toLowerCase().includes(f));
    qs("[data-rows]", list).innerHTML = rows.length ? `<div class="table-wrap"><table class="resp"><thead><tr><th>Server</th><th>System</th><th>Letzte Sicherung</th><th>Gesichert auf</th><th></th></tr></thead><tbody>
      ${rows.map((x) => `<tr><td data-l="Server"><div class="t-main"><span class="tag mono">${x.type === "lxc" ? "CT" : "VM"}</span> ${x.vmid} ${esc(x.name)}</div><div class="t-sub">${x.status === "running" ? '<span class="dot green"></span> läuft' : esc(x.status)}</div></td>
        <td data-l="System" class="small">${esc(x.cluster_name)}<div class="t-sub">${esc(x.node)}</div></td>
        <td data-l="Letzte Sicherung">${fmtTime(x.last_backup, true)}<div class="t-sub">${fmtAgo(x.last_backup)}</div></td>
        <td data-l="Gesichert auf" class="small">${x.backups.map((b) => esc(b.store)).join(", ")}</td>
        <td class="act"><a class="btn sm" href="/wiederherstellen?cluster=${enc(x.cluster)}&vmid=${x.vmid}" data-go>${icon("restore")} Auswählen</a></td></tr>`).join("")}
      </tbody></table></div>` : `<div style="padding:0 16px 16px">${empty("Keine Sicherungen gefunden", guests.length ? "Nichts passt zur Suche." : "Sobald eine VM oder ein Container gesichert wurde, erscheint er hier.", "", "archive")}</div>`;
  };
  qs("[data-q]", list).oninput = (e) => draw(e.target.value.trim().toLowerCase());
  draw("");
}

// Step 2 for one source: the servers backed up on it.
async function restoreSourceList(el, token, x) {
  const head = pageHead(`Sicherungen auf „${esc(x.label)}“`, `${SRC_KIND[x.kind]} · ${esc(x.server_name)} · Datastore ${esc(x.store)}`,
    x.kind === "usb" && x.mounted ? `<button class="btn gray" type="button" data-eject>${icon("eject")} Platte aushängen</button>` : "", ["/wiederherstellen", "Andere Quelle wählen"]);
  if (!x.mounted) {
    el.innerHTML = head + (x.kind === "usb" && x.present
      ? callout("yellow", "Die Platte steckt, ist aber nicht eingehängt", "Zum Wiederherstellen muss sie eingehängt sein.", `<button class="btn sm" type="button" data-mount>${icon("download")} Einhängen</button>`)
      : callout("yellow", x.kind === "usb" ? "Die Platte ist nicht angesteckt" : "Die Quelle ist nicht verfügbar", x.kind === "usb" ? "Bitte die USB-Platte anstecken und die Seite neu laden." : esc(x.error || ""), `<button class="btn gray sm" type="button" data-reload>${icon("refresh")} Neu laden</button>`));
    const m = qs("[data-mount]", el);
    if (m) m.onclick = () => mountSource(m, x, render);
    const rl = qs("[data-reload]", el);
    if (rl) rl.onclick = render;
    return;
  }
  el.innerHTML = head + `<div data-list><div class="panel flex"><span class="spinner"></span> Lade …</div></div>`;
  const ej = qs("[data-eject]", el);
  if (ej) ej.onclick = () => run(ej, async () => { await POST(`/api/pbs/${x.server}/datastores/${enc(x.store)}/unmount`); await new Promise((r) => setTimeout(r, 2500)); go("/wiederherstellen"); }, `„${x.label}“ wird ausgehängt und kann danach abgezogen werden.`);
  let list;
  try { list = (await GET(`/api/restore/source?pbs=${enc(x.server)}&store=${enc(x.store)}`)).guests; }
  catch (e) { qs("[data-list]", el).innerHTML = callout("red", "Inhalt nicht lesbar", esc(e.message)); return; }
  if (token !== pageToken) return;
  qs("[data-list]", el).innerHTML = list.length ? `<div class="panel flush"><div class="panel-head"><div class="panel-title">Server wählen</div></div><div class="table-wrap"><table class="resp"><thead><tr><th>Server</th><th>Neueste Sicherung hier</th><th>Stände</th><th>In Proxmox</th><th></th></tr></thead><tbody>
    ${list.map((g) => `<tr><td data-l="Server"><div class="t-main"><span class="tag mono">${g.type === "lxc" ? "CT" : "VM"}</span> ${g.vmid} ${esc(g.name || "")}</div></td>
      <td data-l="Neueste Sicherung">${fmtTime(g.last_backup, true)}<div class="t-sub">${fmtAgo(g.last_backup)}</div></td>
      <td data-l="Stände" class="small">${g.count}</td>
      <td data-l="In Proxmox" class="small">${g.exists ? `${esc(g.cluster_name)} · ${esc(g.node)}` : '<span class="muted">in keinem verbundenen Proxmox VE</span>'}</td>
      <td class="act"><a class="btn sm" href="${srcHref(x)}&vmid=${g.vmid}&type=${g.type}" data-go>${icon("restore")} Auswählen</a></td></tr>`).join("")}
    </tbody></table></div></div>` : empty("Auf dieser Quelle liegen keine Sicherungen", "", "", "archive");
}

// Step 2 and 3: which backup, then files or the whole guest.
async function restoreGuest(el, token, g, source) {
  const typ = g.type === "lxc" ? "ct" : "vm";
  const kind = typ === "ct" ? "Container" : "VM";
  let src = 0, snaps = [], sel = null;
  const where = g.node ? `${kind} auf ${esc(g.node)} (${esc(g.cluster_name)})` : `${kind}, in keinem verbundenen Proxmox VE – wird in ${esc(g.cluster_name)} wiederhergestellt`;
  el.innerHTML = pageHead(`${g.vmid} ${esc(g.name || "")} wiederherstellen`, where + (source ? ` · Quelle: ${esc(source.label)}` : ""), "",
    source ? [srcHref(source), "Zurück zu „" + source.label + "“"] : ["/wiederherstellen", "Anderen Server wählen"]) +
    `<div class="grid grid-restore">
      <div class="panel flush"><div class="panel-head"><div class="panel-title">1. Stand wählen</div></div>
        ${g.backups.length > 1 ? `<div style="padding:0 16px 12px">${field({ name: "src", label: "Sicherungen auf", type: "select", options: g.backups.map((b, i) => [i, `${b.server_name} / ${b.store} (${b.count})`]) })}</div>` : `<p class="panel-intro">${esc(g.backups[0].server_name)} / ${esc(g.backups[0].store)}</p>`}
        <div data-snaps><div class="row flex"><span class="spinner"></span> Lade …</div></div></div>
      <div data-right></div></div>`;
  const srcSel = qs('[name="src"]', el);
  if (srcSel) srcSel.onchange = () => { src = +srcSel.value; loadSnaps(); };
  const query = () => { const b = g.backups[src]; return `cluster=${enc(g.cluster)}&pbs=${enc(b.server)}&store=${enc(b.store)}&type=${typ}&bid=${g.vmid}&time=${sel}`; };

  const drawRight = () => {
    const right = qs("[data-right]", el);
    if (!sel) { right.innerHTML = `<div class="panel">${empty("Links einen Stand wählen", "", "", "calendar")}</div>`; return; }
    right.innerHTML = `<div class="panel"><div class="panel-head"><div><div class="panel-title">2. Was wiederherstellen?</div><div class="muted small">Stand vom ${fmtTime(sel, true)}</div></div></div>
        <div class="radio-cards">
          <button type="button" class="radio-card" data-files style="text-align:left;font:inherit;color:inherit">${icon("folder")}<span><b>Dateien und Ordner</b><small>Einzelne Dateien aus der Sicherung herunterladen. Der ${kind} läuft dabei normal weiter.</small></span></button>
          <button type="button" class="radio-card" data-whole style="text-align:left;font:inherit;color:inherit">${icon("restore")}<span><b>Ganze${typ === "ct" ? "n Container" : " VM"}</b><small>Als neue${typ === "ct" ? "n Container" : " VM"} neben dem Original oder das Original ersetzen.</small></span></button>
        </div></div><div data-browser></div>`;
    qs("[data-files]", right).onclick = () => fileBrowser(qs("[data-browser]", right), query(), g, typ);
    qs("[data-whole]", right).onclick = () => restoreWhole(g, typ, g.backups[src], sel);
  };

  const loadSnaps = async () => {
    const b = g.backups[src];
    const box = qs("[data-snaps]", el);
    box.innerHTML = `<div class="row flex"><span class="spinner"></span> Lade …</div>`;
    try {
      snaps = (await GET(`/api/guests/snapshots?pbs=${enc(b.server)}&store=${enc(b.store)}&type=${typ}&bid=${g.vmid}`)).snapshots;
    } catch (e) { box.innerHTML = `<div class="row"><div class="form-error grow">${esc(e.message)}</div></div>`; return; }
    if (token !== pageToken) return;
    sel = snaps[0]?.["backup-time"] || null;
    box.innerHTML = `<div class="rows" style="max-height:62vh;overflow:auto">${snaps.map((sn, i) => `
      <label class="row" style="cursor:pointer;align-items:center"><input type="radio" name="snap" value="${sn["backup-time"]}" ${i === 0 ? "checked" : ""} style="accent-color:var(--c600)">
        <div class="grow"><div class="title"><b>${fmtTime(sn["backup-time"], true)}</b> ${i === 0 ? pill("neueste", "blue") : ""}</div>
        <div class="t-sub">${fmtAgo(sn["backup-time"])} · ${fmtBytes(sn.size)}${sn.comment ? " · " + esc(sn.comment) : ""}</div></div>
        ${sn.verification ? (sn.verification.state === "ok" ? pill("geprüft", "green") : pill("defekt", "rose")) : ""}</label>`).join("") || `<div class="row muted">Keine Stände.</div>`}</div>`;
    qsa('[name="snap"]', box).forEach((r) => r.onchange = () => { sel = +r.value; drawRight(); });
    drawRight();
  };
  await loadSnaps();
}

// File browser inside a backup: folders open on click, files download, folders download as zip.
function fileBrowser(box, query, g, typ) {
  const stack = [{ name: typ === "ct" ? "Container" : "VM", path: "" }];
  const typeIcon = { d: "folder", v: "disk", f: "copy", l: "link", h: "link" };
  const show = async () => {
    const cur = stack[stack.length - 1];
    box.innerHTML = `<div class="panel flush"><div class="panel-head"><div class="panel-title">3. Dateien auswählen</div></div>
      <div style="padding:0 16px 12px" class="flex small">${stack.map((s2, i) => i < stack.length - 1 ? `<a href="#" data-crumb="${i}">${esc(s2.name)}</a> /` : `<b>${esc(s2.name)}</b>`).join(" ")}</div>
      <div class="rows" data-entries><div class="row flex"><span class="spinner"></span> Sicherung wird geöffnet …${typ === "vm" && stack.length === 1 ? " Bei VMs startet Proxmox dafür kurz eine Hilfs-VM, das dauert bis zu einer Minute." : ""}</div></div></div>`;
    qsa("[data-crumb]", box).forEach((a) => a.onclick = (e) => { e.preventDefault(); stack.length = +a.dataset.crumb + 1; show(); });
    let r;
    try { r = await GET(`/api/restore/files?${query}${cur.path ? "&path=" + enc(cur.path) : ""}`); }
    catch (e) { qs("[data-entries]", box).innerHTML = `<div class="row"><div class="form-error grow">${esc(e.message)}</div></div>`; return; }
    const list = r.entries.slice().sort((a, b) => (a.type === "f") - (b.type === "f") || a.text.localeCompare(b.text, "de"));
    const dl = (e) => `/api/restore/download?${query}&path=${enc(e.filepath)}&name=${enc(e.text)}${e.type === "f" ? "" : "&dir=1"}`;
    qs("[data-entries]", box).innerHTML = (stack.length > 1 ? `<div class="row" style="cursor:pointer" data-up>${icon("back")} <span class="grow">Eine Ebene zurück</span></div>` : "") +
      (list.map((e, i) => {
        const folder = e.type !== "f" && e.type !== "l" && e.type !== "h";
        const label = e.type === "v" ? (e.text.startsWith("drive-") ? "Platte " + e.text.replace(/^drive-|\.img\.fidx$/g, "") : e.text.replace(/\.pxar\.didx$/, "")) : e.text;
        return `<div class="row" style="align-items:center">${icon(typeIcon[e.type] || "copy")}
          <div class="grow">${folder ? `<a href="#" data-open="${i}"><b>${esc(label)}</b></a>` : `<span>${esc(label)}</span>`}
            <div class="t-sub">${e.type === "f" ? fmtBytes(e.size) + " · " : ""}${e.mtime ? fmtTime(e.mtime, true) : ""}</div></div>
          ${e.type === "v" ? "" : `<a class="btn gray sm" href="${dl(e)}" download>${icon("download")} ${folder ? "Ordner (ZIP)" : "Herunterladen"}</a>`}</div>`;
      }).join("") || `<div class="row muted">Dieser Ordner ist leer.</div>`);
    const up = qs("[data-up]", box);
    if (up) up.onclick = () => { stack.pop(); show(); };
    qsa("[data-open]", box).forEach((a) => a.onclick = (e2) => { e2.preventDefault(); const e = list[+a.dataset.open]; stack.push({ name: e.type === "v" && e.text.startsWith("drive-") ? "Platte " + e.text.replace(/^drive-|\.img\.fidx$/g, "") : e.text.replace(/\.pxar\.didx$/, ""), path: e.filepath }); show(); });
  };
  show();
  box.scrollIntoView({ behavior: "smooth", block: "start" });
}

async function restoreWhole(g, typ, b, time) {
  const kind = typ === "ct" ? "Container" : "VM";
  const ids = new Set();
  let nodes = [g.node];
  try {
    const [gs, st] = await Promise.all([GET("/api/guests"), GET(`/api/pve/${g.cluster}/storages`)]);
    gs.guests.filter((x) => x.cluster === g.cluster).forEach((x) => ids.add(x.vmid));
    nodes = (st.nodes || []).map((n) => n.node);
  } catch (e) { /* keep defaults */ }
  let next = 900 + (g.vmid % 100);
  while (ids.has(next)) next++;
  const homeNode = g.node || nodes[0];
  formDialog({
    title: `${g.vmid} ${g.name} wiederherstellen`, ic: "restore", button: "Wiederherstellen",
    text: `Stand vom <b>${fmtTime(time, true)}</b> aus ${esc(b.server_name)} / ${esc(b.store)}.`,
    fields: [
      { name: "mode", type: "radio", label: "Wie?", value: "new", options: [
        ["new", `Als neue${typ === "ct" ? "r Container" : " VM"}`, "Das Original bleibt unberührt – zum Prüfen oder Vergleichen."],
        ["replace", "Original ersetzen", `${g.vmid} ${g.name} wird mit dem Stand der Sicherung überschrieben.`]] },
      { name: "vmid", label: "Neue ID", type: "number", value: next },
      { name: "node", label: "Ziel-Knoten", type: "select", value: homeNode, options: nodes.map((n) => [n, n]) },
      { name: "storage", label: "Speicher für die Disks", type: "select", options: [["", "Lade …"]] },
      { name: "confirm", label: `Zur Bestätigung ${g.vmid} eintippen`, mono: true, hint: `Alles, was seit dem ${fmtTime(time, true)} in ${g.vmid} ${g.name} geändert wurde, geht verloren. ${kind} vorher in Proxmox herunterfahren.` },
      ...(typ === "vm" ? [{ name: "live", label: "Live-Restore: VM sofort starten, Daten im Hintergrund nachladen", type: "checkbox", wide: true, hint: "Die VM ist nach Sekunden da, ist aber langsam, bis alles zurückgeladen ist." }] : []),
      { name: "start", label: "Danach starten", type: "checkbox", value: false, wide: true, hint: "Bei einer Kopie neben dem Original: Vorsicht wegen gleicher IP und gleichem Namen." },
    ],
    onMount: async (d) => {
      const form = qs("form", d);
      const show = () => {
        const v = formValues(form);
        qs('[data-field="vmid"]', d).hidden = v.mode !== "new";
        qs('[data-field="confirm"]', d).hidden = v.mode !== "replace";
        if (v.mode === "replace") qs('[name="node"]', d).value = homeNode;
        qs('[name="node"]', d).disabled = v.mode === "replace";
      };
      qsa('[name="mode"]', d).forEach((r) => r.onchange = show);
      show();
      const loadSt = async () => {
        try {
          const r = await GET(`/api/pve/${g.cluster}/nodes/${enc(qs('[name="node"]', d).value)}/storages?content=${typ === "ct" ? "rootdir" : "images"}`);
          qs('[name="storage"]', d).innerHTML = r.storages.map((s2) => `<option value="${esc(s2.storage)}">${esc(s2.storage)} (${fmtBytes(s2.avail)} frei)</option>`).join("");
        } catch (e) { showErrors(d, e); }
      };
      qs('[name="node"]', d).onchange = loadSt;
      await loadSt();
    },
    submit: async (v) => {
      const replace = v.mode === "replace";
      if (replace && !(await confirmDialog({ title: `${g.vmid} ${g.name} wirklich ersetzen?`, text: `Der aktuelle Stand wird durch die Sicherung vom ${fmtTime(time, true)} ersetzt. Das lässt sich nicht rückgängig machen.`, button: "Ersetzen" }))) return false;
      const r = await POST("/api/restore", { cluster: g.cluster, node: replace ? homeNode : v.node, pbs: b.server, store: b.store, type: typ, bid: String(g.vmid), time,
        vmid: Number(v.vmid), storage: v.storage, start: v.start, live: !!v.live, replace, confirm: v.confirm });
      showTask(r.source, r.upid, replace ? `${g.vmid} wird ersetzt` : `Wiederherstellung als ${v.vmid}`);
    },
  });
}

// ---------- Restore-Tests ----------
const WD = ["", "Mo", "Di", "Mi", "Do", "Fr", "Sa", "So"];
const RT_STATUS = { ok: ["Erfolgreich", "green"], warning: ["Hinweis", "amber"], error: ["Fehler", "rose"], skipped: ["Übersprungen", "amber"],
  running: ["Läuft", "blue"], pending: ["Wartet", ""], cancelled: ["Abgebrochen", "amber"] };
const rtPill = (st) => { const [l, t] = RT_STATUS[st] || [st, ""]; return pill(l, t, st === "running", st === "running"); };

PAGES["/restoretests"] = async (el, token) => {
  await loadConn();
  const draw = async () => {
    const r = await GET("/api/restoretests");
    if (token !== pageToken) return;
    const plans = r.plans, runs = r.runs, act = r.active;
    const cl = (id) => (state.clusters.find((c) => c.id === id) || {}).name || id;
    el.innerHTML = pageHead("Restore-Tests", "Spielt regelmäßig die neueste Sicherung jedes Servers testweise zurück, startet sie abgeschottet vom Netzwerk, prüft ob das System hochkommt und löscht sie wieder. So weißt du, dass die Sicherungen im Ernstfall funktionieren.",
      `<button class="btn" type="button" data-add>${icon("plus")} Plan anlegen</button>`) +
      (act ? `<div class="panel"><div class="panel-head"><div><div class="panel-title">Läuft gerade: ${esc(act.plan_name)}</div><div class="muted small">gestartet ${fmtTime(act.start)} von ${esc(act.by)}</div></div>
        <button class="btn red sm" type="button" data-cancel>${icon("stop")} Abbrechen</button></div>${rtTable(act, true)}</div>` : "") +
      `<div class="panel flush"><div class="panel-head"><div class="panel-title">Pläne</div></div>
        ${plans.length ? `<div class="table-wrap"><table class="resp"><thead><tr><th>Plan</th><th>Quelle</th><th>Getestet auf</th><th>Zeitplan</th><th>Auswahl</th><th></th></tr></thead><tbody>
        ${plans.map((p) => `<tr class="${p.enabled ? "" : "off"}"><td data-l="Plan"><div class="t-main">${esc(p.name)}</div>${p.enabled ? "" : '<div class="t-sub">Zeitplan aus</div>'}</td>
          <td data-l="Quelle" class="small">${esc(serverName(p.pbs))} / <b>${esc(p.store)}</b></td>
          <td data-l="Getestet auf" class="small">${esc(cl(p.cluster))} · ${esc(p.node)}<div class="t-sub">Disks auf ${esc(p.storage)}</div></td>
          <td data-l="Zeitplan" class="small">${(p.weekdays || []).map((d) => WD[d]).join(", ") || "—"} ${esc(p.time || "")}</td>
          <td data-l="Auswahl" class="small">${p.select === "all" ? "alle Server" : (p.select === "include" ? "nur " : "alle außer ") + esc(p.vmids)}</td>
          <td class="act"><div class="row-actions"><button class="btn sm" type="button" data-run="${p.id}" ${act ? "disabled" : ""}>${icon("play")} Jetzt testen</button>
            <button class="ib" type="button" title="Bearbeiten" aria-label="Bearbeiten" data-edit="${p.id}">${icon("pencil")}</button>
            <button class="ib red" type="button" title="Löschen" aria-label="Löschen" data-del="${p.id}">${icon("trash")}</button></div></td></tr>`).join("")}
        </tbody></table></div>` : `<div style="padding:0 16px 16px">${empty("Noch kein Plan", "Zum Beispiel: jeden Samstag um 02:00 alle Server aus dem Hauptspeicher auf pve1 testen.", `<button class="btn sm" type="button" data-add2>${icon("plus")} Plan anlegen</button>`, "shield")}</div>`}</div>
      <div class="panel flush"><div class="panel-head"><div class="panel-title">Verlauf</div></div>
        ${runs.filter((x) => x.status !== "running").length ? `<div class="rows">${runs.filter((x) => x.status !== "running").map((x) => {
          const n = (st) => x.results.filter((y) => y.status === st).length;
          return `<div class="row" style="cursor:pointer;align-items:center" data-show="${esc(x.id)}"><span class="when">${fmtTime(x.start, true)}</span>${rtPill(x.status)}
            <div class="grow"><div class="title"><b>${esc(x.plan_name)}</b> <span class="muted small">${x.end ? "Dauer " + fmtDur(x.start, x.end) : ""} · ${esc(x.by)}</span></div>
            <div class="flex" style="margin-top:4px">${n("ok") ? pill(n("ok") + " erfolgreich", "green") : ""}${n("warning") ? pill(n("warning") + " Hinweis", "amber") : ""}${n("error") ? pill(n("error") + " Fehler", "rose") : ""}${n("skipped") ? pill(n("skipped") + " übersprungen", "amber") : ""}${x.error ? `<span class="small bad">${esc(x.error)}</span>` : ""}</div></div>${icon("chevron-down")}</div>`;
        }).join("")}</div>` : `<div style="padding:0 16px 16px">${empty("Noch kein Testlauf", "", "", "list")}</div>`}</div>
      <div class="callout"><div class="ct"><div class="ch">So bleibt der Test vom Betrieb getrennt</div><div class="cb">Jeder Server wird unter einer freien Test-ID (ab 990000) mit neuen MAC-Adressen wiederhergestellt. Vor dem Start werden die Netzwerkkarten getrennt, durchgereichte PCI- und USB-Geräte, Bind-Mounts und eingelegte ISOs entfernt, CPU und RAM begrenzt und der Autostart abgeschaltet. Vorher prüft die Appliance, ob Platz und Arbeitsspeicher reichen – sonst wird der Server übersprungen. Nach dem Test wird alles gelöscht; Reste eines abgebrochenen Laufs räumt der nächste Lauf weg.</div></div></div>`;
    qsa("[data-add], [data-add2]", el).forEach((b) => b.onclick = () => rtPlanForm(null, draw));
    qsa("[data-edit]", el).forEach((b) => b.onclick = () => rtPlanForm(plans.find((p) => p.id === b.dataset.edit), draw));
    qsa("[data-del]", el).forEach((b) => b.onclick = async () => {
      const p = plans.find((x) => x.id === b.dataset.del);
      if (await confirmDialog({ title: `Plan „${p.name}“ löschen?`, text: "Der Verlauf bleibt erhalten.", button: "Löschen" }))
        run(b, async () => { await DEL(`/api/restoretests/${p.id}`); await draw(); }, "Gelöscht.");
    });
    qsa("[data-run]", el).forEach((b) => b.onclick = async () => {
      if (await confirmDialog({ title: "Restore-Test jetzt starten?", text: "Die Server werden nacheinander auf dem gewählten Knoten wiederhergestellt und gestartet. Das belastet Speicher und Netzwerk – am besten außerhalb der Arbeitszeit.", button: "Starten", tone: "blue", ic: "play" }))
        run(b, async () => { await POST(`/api/restoretests/${b.dataset.run}/run`); await draw(); }, "Restore-Test gestartet.");
    });
    const cb = qs("[data-cancel]", el);
    if (cb) cb.onclick = async () => { if (await confirmDialog({ title: "Test abbrechen?", text: "Der gerade getestete Server wird noch aufgeräumt.", button: "Abbrechen" })) run(cb, () => POST("/api/restoretests/cancel"), "Wird abgebrochen …"); };
    qsa("[data-show]", el).forEach((row) => row.onclick = () => {
      const x = runs.find((y) => y.id === row.dataset.show);
      openDialog(`<div class="dlg-head"><span class="dlg-ic">${icon("shield")}</span><div class="grow"><div class="dlg-title">${esc(x.plan_name)} · ${fmtTime(x.start, true)}</div><p class="dlg-text">${x.end ? "Dauer " + fmtDur(x.start, x.end) + " · " : ""}gestartet von ${esc(x.by)}</p></div>${rtPill(x.status)}</div>
        <div class="dlg-body">${x.error ? `<div class="form-error">${esc(x.error)}</div>` : ""}${rtTable(x, false)}</div>
        <div class="dlg-foot"><button class="btn gray" type="button" onclick="this.closest('dialog').close()">Schließen</button></div>`, "xwide");
    });
    if (act) kickRunning();
    return !!act;
  };
  el.innerHTML = pageHead("Restore-Tests", "Lade …");
  let active = await draw();
  const loop = async () => { active = await draw(); };
  every(5000, token, async () => { if (active || Math.random() < 0.2) await loop(); });
};

function rtTable(x, live) {
  return `<div class="table-wrap"><table class="resp"><thead><tr><th>Server</th><th>Status</th><th>Sicherung vom</th><th>Dauer</th><th>Ergebnis</th>${live ? "" : "<th>Bild</th>"}</tr></thead><tbody>
    ${x.results.map((r) => `<tr><td data-l="Server"><div class="t-main"><span class="tag mono">${r.type === "ct" ? "CT" : "VM"}</span> ${esc(r.vmid)} ${esc(r.name || "")}</div>${r.test_id ? `<div class="t-sub">Test-ID ${r.test_id}${r.disk_bytes ? " · " + fmtBytes(r.disk_bytes) : ""}</div>` : ""}</td>
      <td data-l="Status">${rtPill(r.status)}${r.phase ? `<div class="t-sub">${esc(r.phase)} …</div>` : ""}</td>
      <td data-l="Sicherung vom" class="small nowrap">${fmtTime(r.backup_time, true)}</td>
      <td data-l="Dauer" class="small nowrap">${r.restore_secs ? "Restore " + fmtSecs(r.restore_secs) : ""}${r.boot_secs ? "<br>Start " + fmtSecs(r.boot_secs) : ""}</td>
      <td data-l="Ergebnis" class="small"><div class="${r.status === "error" ? "bad" : ""}">${esc(r.info || "")}</div>${r.hostname ? `<div class="t-sub">Hostname ${esc(r.hostname)}</div>` : ""}${r.notes ? `<div class="t-sub">Isoliert: ${esc(r.notes)}</div>` : ""}</td>
      ${live ? "" : `<td data-l="Bild">${r.screenshot ? `<a href="/api/restoretests/screenshot?run=${enc(x.id)}&file=${enc(r.screenshot)}" target="_blank" rel="noopener"><img src="/api/restoretests/screenshot?run=${enc(x.id)}&file=${enc(r.screenshot)}" alt="Screenshot ${esc(r.vmid)}" style="width:160px;border:1px solid var(--line);border-radius:var(--r)"></a>` : '<span class="muted small">—</span>'}</td>`}</tr>`).join("") || `<tr><td colspan="6" class="muted">Noch keine Server.</td></tr>`}
  </tbody></table></div>`;
}

function rtPlanForm(p, done) {
  const edit = !!p;
  p = p || { enabled: true, select: "all", weekdays: [6], time: "02:00", max_cores: 4, max_memory: 8192, boot_wait: 180, time_limit: 240, id_base: 990000, cluster: state.clusters[0]?.id, pbs: state.servers[0]?.id };
  formDialog({
    title: edit ? `Plan „${p.name}“ bearbeiten` : "Restore-Test-Plan anlegen", ic: "shield",
    fields: [
      { name: "name", label: "Name", value: p.name, required: true, placeholder: "Wöchentlicher Test", wide: true },
      { type: "html", wide: true, html: `<div class="section-title">Was wird getestet?</div>` },
      { name: "pbs", label: "Backup-Server", type: "select", value: p.pbs, options: state.servers.map((x) => [x.id, x.name]) },
      { name: "store", label: "Datastore", type: "select", options: [["", "Lade …"]] },
      { name: "select", type: "radio", label: "Server", value: p.select, options: [["all", "Alle", "Jeweils die neueste Sicherung"], ["include", "Nur …", "Nur diese IDs"], ["exclude", "Alle außer …", "Diese IDs auslassen"]] },
      { name: "vmids", label: "IDs", value: p.vmids, mono: true, placeholder: "100,101,110", wide: true },
      { type: "html", wide: true, html: `<div class="section-title">Wo wird getestet?</div><div class="hint">Am besten ein Knoten und Speicher mit genug freiem Platz, nicht der Speicher der Produktiv-VMs, wenn er knapp ist.</div>` },
      { name: "cluster", label: "Proxmox VE", type: "select", value: p.cluster, options: state.clusters.map((x) => [x.id, x.name]) },
      { name: "node", label: "Knoten", type: "select", options: [["", "Lade …"]] },
      { name: "storage", label: "Speicher für die Test-Disks", type: "select", options: [["", "Lade …"]] },
      { type: "html", wide: true, html: `<div class="section-title">Wann?</div><div class="flex" data-wd>${[1, 2, 3, 4, 5, 6, 7].map((d) => `<label class="check"><input type="checkbox" class="plain" value="${d}" ${(p.weekdays || []).includes(d) ? "checked" : ""}><span>${WD[d]}</span></label>`).join("")}</div>`, name: "weekdays" },
      { name: "time", label: "Uhrzeit", type: "time", value: p.time },
      { name: "enabled", label: "Zeitplan aktiv", type: "checkbox", value: p.enabled },
      { type: "html", wide: true, html: `<div class="section-title">Grenzen</div>` },
      { name: "max_cores", label: "Höchstens CPU-Kerne", type: "number", min: 0, value: p.max_cores, hint: "0 = wie das Original" },
      { name: "max_memory", label: "Höchstens RAM (MiB)", type: "number", min: 0, value: p.max_memory, hint: "0 = wie das Original" },
      { name: "boot_wait", label: "Wartezeit nach dem Start (s)", type: "number", min: 30, value: p.boot_wait, hint: "Bis zum Screenshot; mit Gast-Agent endet sie früher." },
      { name: "time_limit", label: "Zeitlimit je Restore (Min.)", type: "number", min: 1, value: p.time_limit },
      { name: "id_base", label: "Test-IDs ab", type: "number", min: 100, value: p.id_base, mono: true },
      { name: "keep_failed", label: "Fehlgeschlagene Test-VMs zur Untersuchung behalten (bis zum nächsten Lauf)", type: "checkbox", value: p.keep_failed, wide: true },
    ],
    onMount: async (d) => {
      const form = qs("form", d);
      const show = () => { qs('[data-field="vmids"]', d).hidden = formValues(form).select === "all"; };
      qsa('[name="select"]', d).forEach((r) => r.onchange = show);
      show();
      const fillStores = async (val) => {
        const sel = qs('[name="store"]', d);
        try { const list = await storesOf(qs('[name="pbs"]', d).value); sel.innerHTML = list.map((x) => `<option ${x.name === val ? "selected" : ""}>${esc(x.name)}</option>`).join(""); }
        catch (e) { sel.innerHTML = ""; toastErr(e); }
      };
      const fillStorage = async (val) => {
        const sel = qs('[name="storage"]', d);
        try {
          const r = await GET(`/api/pve/${qs('[name="cluster"]', d).value}/nodes/${enc(qs('[name="node"]', d).value)}/storages?content=images`);
          sel.innerHTML = r.storages.map((x) => `<option value="${esc(x.storage)}" ${x.storage === val ? "selected" : ""}>${esc(x.storage)} (${fmtBytes(x.avail)} frei)</option>`).join("");
        } catch (e) { sel.innerHTML = ""; }
      };
      const fillNodes = async (val, sval) => {
        const sel = qs('[name="node"]', d);
        try {
          const r = await GET(`/api/pve/${qs('[name="cluster"]', d).value}/storages`);
          sel.innerHTML = (r.nodes || []).map((n) => `<option ${n.node === val ? "selected" : ""}>${esc(n.node)}</option>`).join("");
        } catch (e) { sel.innerHTML = ""; }
        await fillStorage(sval);
      };
      qs('[name="pbs"]', d).onchange = () => fillStores("");
      qs('[name="cluster"]', d).onchange = () => fillNodes("", "");
      qs('[name="node"]', d).onchange = () => fillStorage("");
      await Promise.all([fillStores(p.store), fillNodes(p.node, p.storage)]);
    },
    submit: async (v, d) => {
      v.weekdays = qsa("[data-wd] input", d).filter((x) => x.checked).map((x) => Number(x.value));
      ["max_cores", "max_memory", "boot_wait", "time_limit", "id_base"].forEach((k) => { v[k] = Number(v[k]) || 0; });
      if (edit) await PUT(`/api/restoretests/${p.id}`, v); else await POST("/api/restoretests", v);
      toast("Gespeichert."); done();
    },
  });
}

// ---------- Sicherungsjobs ----------
PAGES["/jobs"] = async (el, token) => {
  await loadConn();
  if (!state.clusters.length) {
    el.innerHTML = pageHead("Sicherungsjobs") + empty("Kein Proxmox VE verbunden", "Sicherungsjobs laufen in Proxmox VE. Zuerst unter Verbindungen eintragen.", `<a class="btn" href="/verbindungen" data-go>Verbindungen</a>`, "calendar");
    return;
  }
  const draw = async () => {
    const all = await Promise.all(state.clusters.map(async (c) => { try { return { c, r: await GET(`/api/pve/${c.id}/jobs`) }; } catch (e) { return { c, err: e.message }; } }));
    if (token !== pageToken) return;
    el.innerHTML = pageHead("Sicherungsjobs", "Zeitgesteuerte Sicherungen in Proxmox VE (Rechenzentrum → Backup). Ziel ist ein Backup-Speicher, z. B. ein Datastore dieser Appliance.") +
      all.map(({ c, r, err }) => `<div class="panel flush"><div class="panel-head"><div class="panel-title">${esc(c.name)}</div><div class="actions"><button class="btn" type="button" data-add="${c.id}">${icon("plus")} Job anlegen</button></div></div>
        ${err ? `<div style="padding:0 16px 16px">${err.includes("fehlen Rechte") ? permCallout(c, (err.match(/\(([^)]*)\)/) || ["", ""])[1].split(", ").filter(Boolean)) : callout("red", "Nicht erreichbar", esc(err))}</div>` : r.jobs.length ? `<div class="table-wrap"><table class="resp"><thead><tr><th>Job</th><th>Zeitplan</th><th>Was</th><th>Ziel</th><th>Aufbewahrung</th><th></th></tr></thead><tbody>
        ${r.jobs.map((j) => { const names = Object.fromEntries(r.guests.map((g) => [String(g.vmid), g.name])); const ids = (j.vmid || j.exclude || "").split(",").filter(Boolean);
          return `<tr class="${j.enabled === 0 ? "off" : ""}"><td data-l="Job"><div class="t-main">${esc(j.comment || j.id)} ${j.enabled === 0 ? pill("aus") : ""}</div><div class="t-sub mono">${esc(j.id)}</div></td>
          <td data-l="Zeitplan"><span class="mono">${esc(j.schedule)}</span>${j["next-run"] ? `<div class="t-sub">nächster: ${fmtTime(j["next-run"])}</div>` : ""}</td>
          <td data-l="Was" class="small">${j.all ? (j.exclude ? "alle außer " + ids.map((i) => `${i} ${esc(names[i] || "")}`).join(", ") : "alle VMs & Container") : ids.map((i) => `${i} ${esc(names[i] || "")}`).join(", ")}${j.node ? `<div class="t-sub">nur Knoten ${esc(j.node)}</div>` : ""}</td>
          <td data-l="Ziel"><b>${esc(j.storage)}</b><div class="t-sub">${esc({ snapshot: "Snapshot", suspend: "Anhalten", stop: "Stopp" }[j.mode] || j.mode || "Snapshot")}</div></td>
          <td data-l="Aufbewahrung" class="small">${esc((j["prune-backups"] || "").replace(/keep-/g, "").replace(/,/g, ", ")) || '<span class="muted">vom Speicher</span>'}</td>
          <td class="act"><div class="row-actions"><button class="btn gray sm" type="button" data-run="${c.id}|${esc(j.id)}">${icon("play")} Jetzt</button>
            <button class="ib" type="button" title="Bearbeiten" aria-label="Bearbeiten" data-edit="${c.id}|${esc(j.id)}">${icon("pencil")}</button>
            <button class="ib red" type="button" title="Löschen" aria-label="Löschen" data-del="${c.id}|${esc(j.id)}">${icon("trash")}</button></div></td></tr>`; }).join("")}</tbody></table></div>` :
          `<div style="padding:0 16px 16px">${empty("Noch kein Sicherungsjob", "Zum Beispiel jede Nacht um 21:00 alle VMs auf den Backup-Server.", "", "calendar")}</div>`}</div>`).join("");
    bindCopy(el);
    const find = (v) => { const [cid, jid] = v.split("|"); const x = all.find((a) => a.c.id === cid); return { x, job: x.r?.jobs.find((j) => j.id === jid), jid }; };
    qsa("[data-add]", el).forEach((b) => b.onclick = () => jobForm(all.find((a) => a.c.id === b.dataset.add), null, draw));
    qsa("[data-edit]", el).forEach((b) => b.onclick = () => { const f = find(b.dataset.edit); jobForm(f.x, f.job, draw); });
    qsa("[data-run]", el).forEach((b) => b.onclick = () => run(b, async () => {
      const f = find(b.dataset.run);
      const r = await POST(`/api/pve/${f.x.c.id}/jobs/${enc(f.jid)}/run`);
      if (r.errors?.length) toast("Teilweise gestartet: " + r.errors.join("; "), "error");
      showTask("pve:" + f.x.c.id, r.upids[0], "Sicherungsjob " + f.jid);
    }));
    qsa("[data-del]", el).forEach((b) => b.onclick = async () => {
      const f = find(b.dataset.del);
      if (await confirmDialog({ title: `Sicherungsjob ${f.job.comment || f.jid} löschen?`, text: "Vorhandene Sicherungen bleiben erhalten.", button: "Löschen" }))
        run(b, async () => { await DEL(`/api/pve/${f.x.c.id}/jobs/${enc(f.jid)}`); await draw(); }, "Gelöscht.");
    });
  };
  el.innerHTML = pageHead("Sicherungsjobs", "Lade …");
  await draw();
};

function jobForm(x, j, done) {
  const edit = !!j;
  const { c, r } = x;
  const targets = r.storages.filter((s) => (s.content || "").includes("backup"));
  const sel = j ? (j.all ? (j.exclude ? "exclude" : "all") : "include") : "all";
  const chosen = new Set((j?.vmid || j?.exclude || "").split(",").filter(Boolean));
  const guestsHTML = `<div class="field wide" data-field="vmids"><span class="fl">VMs & Container</span><div class="section" style="max-height:240px;overflow:auto;gap:6px">
    ${r.guests.filter((g) => !g.template).sort((a, b) => a.vmid - b.vmid).map((g) => `<label class="check"><input type="checkbox" class="plain" data-vm="${g.vmid}" ${chosen.has(String(g.vmid)) ? "checked" : ""}><span>${g.vmid} ${esc(g.name)} <span class="muted small">${g.type === "lxc" ? "CT" : "VM"} · ${esc(g.node)}</span></span></label>`).join("")}
    </div><span class="err" hidden></span></div>`;
  formDialog({
    title: edit ? "Sicherungsjob bearbeiten" : `Sicherungsjob in ${c.name}`, ic: "calendar",
    fields: [
      { name: "comment", label: "Beschreibung", value: j?.comment || "", wide: true, placeholder: "Jede Nacht alles" },
      { name: "schedule", label: "Zeitplan", value: j?.schedule || "21:00", required: true, mono: true, hint: "z. B. 21:00 · mon..fri 22:30 · sat 02:00 · */4:00" },
      { name: "storage", label: "Ziel", type: "select", value: j?.storage, options: targets.map((s) => [s.storage, `${s.storage} (${s.type === "pbs" ? "Backup-Server" : s.type})`]) },
      { name: "mode", label: "Modus", type: "select", value: j?.mode || "snapshot", options: [["snapshot", "Snapshot (läuft weiter)"], ["suspend", "Anhalten"], ["stop", "Herunterfahren"]] },
      { name: "node", label: "Nur auf Knoten", type: "select", value: j?.node || "", options: [["", "alle Knoten"], ...(r.nodes || []).map((n) => [n.node, n.node])] },
      { name: "selection", type: "radio", label: "Was sichern?", value: sel, options: [["all", "Alles", "Auch künftige VMs"], ["exclude", "Alles außer …", "Auswahl ausnehmen"], ["include", "Nur …", "Nur die Auswahl"]] },
      { type: "html", wide: true, html: guestsHTML, name: "vmids_box" },
      { name: "prune_backups", label: "Aufbewahrung im Job (optional)", value: j?.["prune-backups"] || "", mono: true, wide: true, placeholder: "keep-last=3,keep-daily=7,keep-weekly=4", hint: "Leer = die Aufbewahrung des Datastores (empfohlen, unter Datastores einstellen)." },
      { name: "notes_template", label: "Notiz an der Sicherung", value: j?.["notes-template"] ?? "{{guestname}}", mono: true, hint: "{{guestname}}, {{node}}, {{vmid}}, {{cluster}}" },
      { name: "enabled", label: "Job aktiv", type: "checkbox", value: j ? j.enabled !== 0 : true },
    ],
    onMount: (d) => {
      const form = qs("form", d);
      const show = () => { qs('[data-field="vmids"]', d).hidden = formValues(form).selection === "all"; };
      qsa('[name="selection"]', d).forEach((x2) => x2.onchange = show);
      show();
    },
    submit: async (v, d) => {
      v.vmids = qsa("[data-vm]", d).filter((x2) => x2.checked).map((x2) => x2.dataset.vm).join(",");
      if (edit) await PUT(`/api/pve/${c.id}/jobs/${enc(j.id)}`, v); else await POST(`/api/pve/${c.id}/jobs`, v);
      toast("Gespeichert."); done();
    },
  });
}

// ---------- Aufgaben ----------
PAGES["/aufgaben"] = async (el, token) => {
  let kind = "all";
  const draw = async (fresh) => {
    const r = await GET("/api/overview" + (fresh ? "?fresh=1" : ""));
    if (token !== pageToken) return;
    let tasks = r.overview.tasks || [];
    if (kind === "errors") tasks = tasks.filter((t) => t.endtime && t.status !== "OK" && !(t.status || "").startsWith("WARNINGS"));
    if (kind === "backup") tasks = tasks.filter((t) => t.type === "vzdump");
    if (kind === "running") tasks = tasks.filter((t) => !t.endtime);
    qs("[data-list]", el).innerHTML = tasks.length ? `<div class="panel flush"><div class="table-wrap"><table class="resp"><thead><tr><th>Start</th><th>Aufgabe</th><th>Server</th><th>Dauer</th><th>Status</th></tr></thead><tbody>
      ${tasks.map((t) => `<tr style="cursor:pointer" data-task="${esc(t.source)}|${esc(t.upid)}|${esc(taskLabel(t.type) + " " + (t.id || ""))}"><td data-l="Start" class="nowrap mono small">${fmtTime(t.starttime, true)}</td>
        <td data-l="Aufgabe"><div class="t-main">${esc(taskLabel(t.type))}</div><div class="t-sub mono">${esc(t.id || "")}</div></td>
        <td data-l="Server" class="small">${esc(t.server)}${t.node ? `<div class="t-sub">${esc(t.node)}</div>` : ""}</td>
        <td data-l="Dauer" class="small nowrap">${t.endtime ? fmtDur(t.starttime, t.endtime) : "…"}</td>
        <td data-l="Status">${taskPill(t)}${t.endtime && t.status !== "OK" ? `<div class="t-sub">${esc(t.status)}</div>` : ""}</td></tr>`).join("")}</tbody></table></div></div>` : empty("Keine Aufgaben", "", "", "list");
    qsa("[data-task]", el).forEach((row) => row.onclick = () => { const [s, u, l] = row.dataset.task.split("|"); showTask(s, u, l); });
  };
  el.innerHTML = pageHead("Aufgaben", "Die letzten Aufgaben aller Backup-Server sowie Sicherungen und Wiederherstellungen aus Proxmox VE. Klick zeigt das Protokoll.",
    `<div class="seg" role="group" aria-label="Filter">${[["all", "Alle"], ["running", "Laufend"], ["backup", "Sicherungen"], ["errors", "Fehler"]].map(([k, l]) => `<button type="button" data-kind="${k}" aria-pressed="${k === kind}">${l}</button>`).join("")}</div>
     <button class="btn gray" type="button" data-refresh>${icon("refresh")} Aktualisieren</button>`) + `<div data-list><div class="panel flex"><span class="spinner"></span> Lade …</div></div>`;
  qsa("[data-kind]", el).forEach((b) => b.onclick = () => { kind = b.dataset.kind; qsa("[data-kind]", el).forEach((x) => x.setAttribute("aria-pressed", x === b)); draw(false); });
  qs("[data-refresh]", el).onclick = (e) => run(e.currentTarget, () => draw(true));
  await draw(false);
  every(15000, token, () => draw(true));
};

// ---------- Ereignisse ----------
PAGES["/ereignisse"] = async (el, token) => {
  let sev = "", list = [], more = false;
  const load = async (append) => {
    const before = append && list.length ? list[list.length - 1].id : 0;
    const r = await GET(`/api/events?severity=${sev}&before=${before}`);
    if (token !== pageToken) return;
    list = append ? list.concat(r.events) : r.events;
    more = r.more;
    const open = new Set(qsa("details.ev-details[open]", el).map((x) => x.dataset.id));
    qs("[data-list]", el).innerHTML = list.length ? `<div class="panel flush"><div class="rows">${list.map((e) => eventRow(e, false)).join("")}</div></div>${more ? `<div style="text-align:center;margin:12px"><button class="btn gray" type="button" data-more>Ältere laden</button></div>` : ""}` : empty("Keine Ereignisse", "", "", "bell");
    qsa("details.ev-details", el).forEach((x, i) => { x.dataset.id = list[i]?.id; if (open.has(String(list[i]?.id))) x.open = true; });
    const m = qs("[data-more]", el);
    if (m) m.onclick = () => run(m, () => load(true));
  };
  el.innerHTML = pageHead("Ereignisse", "Was die Appliance beobachtet hat: fehlgeschlagene Aufgaben, Wechselplatten, Freigaben, volle Datastores. Benachrichtigungen dafür stellst du unter Einstellungen ein.",
    `<select data-sev aria-label="Schweregrad" style="width:auto"><option value="">Alle Stufen</option><option value="warning">ab Warnung</option><option value="critical">nur Kritisch</option></select>`) + `<div data-list></div>`;
  qs("[data-sev]", el).onchange = (e) => { sev = e.target.value; load(false); };
  await load(false);
  every(30000, token, () => load(false));
};

// ---------- Einstellungen ----------
PAGES["/einstellungen"] = async (el) => {
  const [sys, set] = await Promise.all([GET("/api/system"), GET("/api/settings")]);
  const n = set.notify;
  el.innerHTML = pageHead("Einstellungen") + `<div class="grid">
    <div class="panel"><div class="panel-head"><div class="panel-title">Benachrichtigungen</div></div>
      <form class="form" data-notify novalidate><div class="form-error" hidden></div>
        <div class="form-grid">
          ${field({ name: "min_severity", label: "Melden ab", type: "select", value: n.min_severity, options: [["info", "Alles – auch erfolgreiche Sicherungen und „Platte kann gewechselt werden“"], ["warning", "Nur Warnungen und Fehler"], ["critical", "Nur Fehler"]], wide: true })}
        </div>
        <div class="section"><div class="form-grid">
          ${field({ name: "mail_enabled", label: "Per E-Mail", type: "checkbox", value: n.mail_enabled, wide: true })}
          ${field({ name: "smtp_host", label: "SMTP-Server", value: n.smtp_host, mono: true, placeholder: "smtp.example.org" })}
          ${field({ name: "smtp_port", label: "Port", type: "number", value: n.smtp_port })}
          ${field({ name: "smtp_security", label: "Verschlüsselung", type: "select", value: n.smtp_security, options: [["starttls", "STARTTLS (587)"], ["tls", "TLS (465)"], ["none", "keine"]] })}
          ${field({ name: "smtp_user", label: "Benutzer", value: n.smtp_user })}
          ${field({ name: "smtp_password", label: "Passwort", type: "password", placeholder: set.has_smtp_password ? "unverändert" : "" })}
          ${field({ name: "mail_from", label: "Absender", value: n.mail_from, placeholder: "backup@example.org" })}
          ${field({ name: "mail_to", label: "Empfänger", value: n.mail_to, placeholder: "ich@example.org, …", wide: true })}
        </div></div>
        <div class="section"><div class="form-grid">
          ${field({ name: "hook_enabled", label: "Per Push (ntfy, Gotify, Webhook)", type: "checkbox", value: n.hook_enabled, wide: true })}
          ${field({ name: "hook_kind", label: "Art", type: "select", value: n.hook_kind, options: [["ntfy", "ntfy"], ["gotify", "Gotify"], ["json", "Webhook (JSON)"]] })}
          ${field({ name: "hook_url", label: "URL", value: n.hook_url, mono: true, placeholder: "https://ntfy.sh/mein-thema", wide: true })}
          ${field({ name: "hook_token", label: "Token (optional)", type: "password", placeholder: set.has_hook_token ? "unverändert" : "" })}
        </div></div>
        <div class="section"><div class="section-title">So sieht die Mail aus</div>
          <div class="hint">Grün bei Erfolg, Gelb bei Warnungen (z. B. einzelne VMs nicht gesichert), Rot bei Fehlern – jeweils mit der Liste der gesicherten VMs und Container.</div>
          <div class="flex"><span class="small muted">Vorschau:</span>
            <a class="btn gray sm" href="/api/settings/preview?status=ok" target="_blank" rel="noopener"><span class="dot green"></span> Erfolg</a>
            <a class="btn gray sm" href="/api/settings/preview?status=warning" target="_blank" rel="noopener"><span class="dot amber"></span> Warnung</a>
            <a class="btn gray sm" href="/api/settings/preview?status=error" target="_blank" rel="noopener"><span class="dot rose"></span> Fehler</a>
            <a class="btn gray sm" href="/api/settings/preview?status=usb" target="_blank" rel="noopener"><span class="dot green"></span> USB fertig</a></div></div>
        <div class="flex"><button class="btn" type="submit">Speichern</button>
          <select data-test-status aria-label="Art der Testnachricht" style="width:auto;min-width:0"><option value="ok">Test: Erfolg</option><option value="warning">Test: Warnung</option><option value="error">Test: Fehler</option><option value="usb">Test: USB fertig</option></select>
          <button class="btn gray" type="button" data-test>Testnachricht senden</button></div>
      </form></div>
    <div>
      <div class="panel"><div class="panel-head"><div class="panel-title">Appliance</div></div>
        <table class="kv"><tr><td>Version</td><td>PK95 ProxBackup ${esc(sys.version)}</td></tr>
          <tr><td>Hostname</td><td class="mono">${esc(sys.hostname)}</td></tr>
          <tr><td>Adressen</td><td class="mono">${(sys.ips || []).map(esc).join("<br>") || "—"}</td></tr>
          <tr><td>Backup-Server</td><td>${sys.local_pbs ? "Proxmox Backup Server ist in dieser VM installiert (Weboberfläche: Port 8007, Anmeldung root)" : "kein lokaler PBS"}</td></tr>
          <tr><td>Angemeldet</td><td>${esc(sys.user)}</td></tr></table>
        ${sys.local_pbs && (sys.ips || []).length ? `<div class="mt"><a class="btn gray" href="https://${esc(sys.ips[0])}:8007" target="_blank" rel="noopener">${icon("external")} PBS-Oberfläche öffnen</a></div>` : ""}</div>
      <div class="panel"><div class="panel-head"><div class="panel-title">Zugriff & Reverse Proxy</div></div>
        <form class="form" data-access novalidate><div class="form-error" hidden></div>
          ${set.seen_proxy && set.seen_proxy.url ? `<div class="callout" style="margin:0"><div class="ct"><div class="ch">Reverse Proxy erkannt</div><div class="cb">${esc(set.seen_proxy.url)} über ${esc(set.seen_proxy.proxy_ip)}</div></div>
            <div class="flex"><button class="btn gray sm" type="button" data-adopt>Übernehmen</button></div></div>` : ""}
          <div class="form-grid">
            ${field({ name: "public_url", label: "Adresse der Oberfläche", value: set.access.public_url, placeholder: set.public_url, mono: true, wide: true, hint: "Für die Links in Mails und Push-Nachrichten. Leer = automatisch (jetzt: " + set.public_url + ")." })}
            ${field({ name: "trusted_proxies", label: "IP des Reverse Proxys", value: (set.access.trusted_proxies || []).join(", "), mono: true, wide: true, placeholder: "z. B. 10.10.250.95", hint: "Dann gilt die Login-Sperre für die echte Adresse des Aufrufers statt für den Proxy. Du kommst gerade von " + set.client_ip + (set.client_ip !== set.remote_ip ? " (über " + set.remote_ip + ")" : "") + "." })}
          </div>
          <div class="hint">Proxy-Ziel: <code>https://${esc((sys.ips || [])[0] || "IP")}:443</code> (Zertifikatsprüfung aus, selbst signiert) oder <code>http://${esc((sys.ips || [])[0] || "IP")}:80</code>; der Proxy muss dann <code>X-Forwarded-Proto: https</code> mitschicken. Websockets sind nicht nötig.</div>
          <div><button class="btn" type="submit">Speichern</button></div></form></div>
      <div class="panel"><div class="panel-head"><div class="panel-title">SSH-Schlüssel der Appliance</div></div>
        <p class="panel-intro">Für NFS/SMB-Freigaben auf einem externen PBS. Auf dem PBS eintragen:</p>${codeBlock(`mkdir -p /root/.ssh && echo '${sys.ssh_key}' >> /root/.ssh/authorized_keys`)}</div>
      <div class="panel"><div class="panel-head"><div class="panel-title">Passwort ändern</div></div>
        <form class="form" data-pw novalidate><div class="form-error" hidden></div><div class="form-grid">
          ${field({ name: "current", label: "Aktuelles Passwort", type: "password", autocomplete: "current-password" })}
          ${field({ name: "new", label: "Neues Passwort", type: "password", hint: "Mindestens 10 Zeichen.", autocomplete: "new-password" })}
        </div><div><button class="btn" type="submit">Passwort ändern</button></div></form></div>
    </div></div>`;
  bindCopy(el);
  const nf = qs("[data-notify]", el);
  const nv = () => { const v = formValues(nf); v.smtp_port = Number(v.smtp_port) || 587; return { notify: v }; };
  nf.onsubmit = async (e) => { e.preventDefault(); try { showErrors(nf, null); await PUT("/api/settings", nv()); toast("Gespeichert."); } catch (err) { showErrors(nf, err); } };
  qs("[data-test]", el).onclick = (e) => run(e.currentTarget, async () => { showErrors(nf, null); try { await POST("/api/settings/test?status=" + qs("[data-test-status]", el).value, nv()); toast("Testnachricht verschickt."); } catch (err) { showErrors(nf, err); } });
  const af = qs("[data-access]", el);
  const av = () => { const v = formValues(af); return { public_url: v.public_url.trim(), trusted_proxies: v.trusted_proxies.split(/[\s,]+/).filter(Boolean) }; };
  af.onsubmit = async (e) => { e.preventDefault(); try { showErrors(af, null); await PUT("/api/settings/access", av()); toast("Gespeichert."); render(); } catch (err) { showErrors(af, err); } };
  const adopt = qs("[data-adopt]", el);
  if (adopt) adopt.onclick = () => { qs('[name="public_url"]', af).value = set.seen_proxy.url; qs('[name="trusted_proxies"]', af).value = set.seen_proxy.proxy_ip; af.requestSubmit(); };
  const pf = qs("[data-pw]", el);
  pf.onsubmit = async (e) => { e.preventDefault(); try { showErrors(pf, null); await POST("/api/account/password", formValues(pf)); pf.reset(); toast("Passwort geändert. Andere Sitzungen wurden abgemeldet."); } catch (err) { showErrors(pf, err); } };
};

render();
