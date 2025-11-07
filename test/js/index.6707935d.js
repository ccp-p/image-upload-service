// js/main.js
class MainApp {
    constructor() {
        this.init();
    }

    init() {
        console.log('Main application initialized');
        this.bindEvents();
        this.loadFeatures();
    }

    bindEvents() {
        document.addEventListener('DOMContentLoaded', () => {
            console.log('DOM loaded');
        });
    }

    loadFeatures() {
        const features = document.querySelectorAll('.feature-card');
        features.forEach((feature, index) => {
            feature.style.animationDelay = `${index * 0.1}s`;
        });
    }

    static getInstance() {
        if (!MainApp.instance) {
            MainApp.instance = new MainApp();
        }
        return MainApp.instance;
    }
}

// Initialize the app
document.addEventListener('DOMContentLoaded', () => {
    const app = MainApp.getInstance();
});