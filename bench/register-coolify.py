#!/usr/bin/env python3
"""Creates Coolify's first account in a real browser, as a person would.

Usage: register-coolify.py URL
URL is the panel reached through an ssh tunnel, e.g. http://127.0.0.1:18000.
The password is random and thrown away: the box is deleted after the run.
"""
import secrets
import sys

from playwright.sync_api import sync_playwright

url = sys.argv[1].rstrip("/")
password = secrets.token_urlsafe(18) + "aA1!"
with sync_playwright() as p:
    browser = p.chromium.launch()
    page = browser.new_page(viewport={"width": 1280, "height": 1600})
    page.goto(url + "/register")
    # Field ids carry a random suffix, e.g. email-hlunvgeds7od.
    page.wait_for_selector("input[id^='email-']", timeout=60000)
    page.fill("input[id^='name-']", "Bench")
    page.fill("input[id^='email-']", "bench@example.com")
    page.fill("input[id^='password-']", password)
    page.fill("input[id^='password_confirmation-']", password)
    page.click("button[type=submit]")
    page.wait_for_url(lambda u: "/register" not in u, timeout=60000)
    print("after register:", page.url)
    browser.close()
